# SPDX-License-Identifier: Apache-2.0
"""Tangle AdapterService over QRMI-managed resources (phase1-build-plan.md M8).

The adapter owns everything the spec demands that QRMI does not provide:
idempotency keys, the task FSM, error taxonomy, usage records, and a
per-resource single-worker queue (QRMI tasks have no idempotent submit and
no queue-position contract). QASM is validated locally before any QRMI
call so invalid programs fail as INVALID_PROGRAM, not as vendor noise.
"""

from __future__ import annotations

import threading
import time
import uuid
from concurrent.futures import ThreadPoolExecutor

import grpc
from google.protobuf import timestamp_pb2
from qiskit import qasm3

from tangle.adapter.v1alpha1 import adapter_pb2 as pb
from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

VENDOR = "qrmi"
MODALITY = "gate-model"
PROGRAM_FORMATS = ("openqasm3",)
BILLING_UNITS = ("shots", "tasks")
MAX_SHOTS = 100_000
DELAY_PARAM = "rabi.sim/delay-ms"

_TERMINAL = {
    pb.TaskStatus.State.SUCCEEDED,
    pb.TaskStatus.State.FAILED,
    pb.TaskStatus.State.CANCELLED,
}


def _ts(seconds: float) -> timestamp_pb2.Timestamp:
    ts = timestamp_pb2.Timestamp()
    ts.FromNanoseconds(int(seconds * 1e9))
    return ts


class _Categorized(Exception):
    def __init__(self, category, message: str):
        super().__init__(message)
        self.detail = pb.ErrorDetail(category=category, retriable=False,
                                     vendor_message=message[:500])


class _Task:
    def __init__(self, task_id: str, target_id: str, shots: int):
        self.task_id = task_id
        self.target_id = target_id
        self.shots = shots
        self.qrmi_task_id: str | None = None
        self.failed: pb.ErrorDetail | None = None
        self.cancel_requested = False
        self.cancelled_before_run = False
        self.updated_at = time.time()


class QrmiAdapterService(pb_grpc.AdapterServiceServicer):
    """Serves QRMI resources. `resources` maps target_id → backend.

    Doubles as the shared adapter chassis (docs/decisions.md D-043): any
    backend exposing describe/start/status/result/stop can ride it —
    subclasses override the class attributes and extensions hook.
    """

    VENDOR = VENDOR
    MAX_SHOTS = MAX_SHOTS
    SNAPSHOT_PREFIX = "qrmi"

    def __init__(self, resources: dict[str, object]):
        self._resources = resources
        self._described = {tid: r.describe() for tid, r in resources.items()}
        self._lock = threading.Lock()
        self._tasks: dict[str, _Task] = {}
        self._by_key: dict[tuple[str, str], str] = {}
        self._queues = {tid: ThreadPoolExecutor(max_workers=1) for tid in resources}
        self._started_at = time.time()

    # -- discovery ----------------------------------------------------------

    def _info(self, target_id: str) -> pb.TargetInfo:
        d = self._described[target_id]
        return pb.TargetInfo(
            target_id=target_id, display_name=d["resource_id"], vendor=self.VENDOR,
            modality=MODALITY, simulator=False, technology=d["technology"],
        )

    def _resource(self, target_id: str, context):
        r = self._resources.get(target_id)
        if r is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"target {target_id!r} not served here")
        return r

    def ListTargets(self, request, context):
        return pb.ListTargetsResponse(targets=[self._info(t) for t in self._resources])

    def GetCapabilities(self, request, context):
        self._resource(request.target_id, context)
        d = self._described[request.target_id]
        return pb.Capabilities(
            target=self._info(request.target_id),
            num_qubits=int(d["num_qubits"]),
            program_formats=list(PROGRAM_FORMATS),
            max_shots=self.MAX_SHOTS,
            sessions=False,
            cancellation=True,
            billing_units=list(BILLING_UNITS),
            coupling_class="loose",
            cloud_queue=bool(d["cloud"]),
            vendor_extensions=self._extensions(d),
        )

    def _extensions(self, d: dict) -> dict[str, str]:
        return {
            "technology": d["technology"],
            "cloud": "true" if d["cloud"] else "false",
            "qrmi-resource-type": d["resource_type"],
        }

    def GetDeviceState(self, request, context):
        self._resource(request.target_id, context)
        d = self._described[request.target_id]
        metrics = [
            pb.Metric(
                name=m["name"], value=float(m["value"]),
                qubits=[int(q) for q in m.get("qubits", [])],
                methodology=f'{m["methodology"]} ({m.get("upstream", "unknown")})',
            )
            for m in d["metrics"]
        ]
        snapshot = pb.CalibrationSnapshot(
            snapshot_id=f'{self.SNAPSHOT_PREFIX}-{d["resource_type"]}-{int(self._started_at)}',
            source=f'{self.SNAPSHOT_PREFIX}:{d["resource_type"]}/{d["resource_id"]}',
            measured_at=_ts(self._started_at),
            metrics=metrics,
        )
        return pb.DeviceState(
            status=pb.DeviceState.Status.ONLINE,
            queue_depth=0,
            calibration=snapshot,
            observed_at=_ts(time.time()),
        )

    # -- execution ----------------------------------------------------------

    def SubmitTask(self, request, context):
        resource = self._resource(request.target.target_id, context)
        if not request.idempotency_key:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "idempotency_key is required")
        if request.payload.WhichOneof("body") != "inline":
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "inline payloads only")

        with self._lock:
            key = (request.target.target_id, request.idempotency_key)
            if key in self._by_key:
                return pb.TaskHandle(target=request.target, task_id=self._by_key[key])
            task = _Task(str(uuid.uuid4()), request.target.target_id, request.shots)
            self._tasks[task.task_id] = task
            self._by_key[key] = task.task_id

        payload_format = request.payload.format
        payload_inline = request.payload.inline
        shots = request.shots

        def start() -> None:
            with self._lock:
                if task.cancel_requested:
                    task.cancelled_before_run = True
                    task.updated_at = time.time()
                    return
            try:
                if payload_format not in PROGRAM_FORMATS:
                    raise _Categorized(pb.ErrorDetail.Category.CAPABILITY_MISMATCH,
                                       f"format {payload_format!r} unsupported")
                if shots and shots > self.MAX_SHOTS:
                    raise _Categorized(
                        pb.ErrorDetail.Category.CAPABILITY_MISMATCH,
                        f"{shots} shots exceeds declared max_shots {self.MAX_SHOTS}")
                qasm = _validate_qasm(payload_inline)
                qrmi_id = resource.start(qasm, shots or 1024)
                with self._lock:
                    task.qrmi_task_id = qrmi_id
                    task.updated_at = time.time()
                    if task.cancel_requested:
                        try:
                            resource.stop(qrmi_id)
                        except Exception:  # noqa: BLE001 — cancel is best-effort
                            pass
            except _Categorized as exc:
                with self._lock:
                    task.failed = exc.detail
                    task.updated_at = time.time()
            except Exception as exc:  # noqa: BLE001 — map to taxonomy, never bare
                with self._lock:
                    task.failed = pb.ErrorDetail(
                        category=pb.ErrorDetail.Category.VENDOR_ERROR, retriable=True,
                        vendor_code=type(exc).__name__, vendor_message=str(exc)[:500])
                    task.updated_at = time.time()

        try:
            delay_ms = int(request.parameters.get(DELAY_PARAM, "0"))
        except ValueError:
            delay_ms = 0

        def run_task() -> None:
            if delay_ms > 0:
                deadline = time.time() + delay_ms / 1000.0
                while time.time() < deadline:
                    with self._lock:
                        if task.cancel_requested:
                            task.cancelled_before_run = True
                            task.updated_at = time.time()
                            return
                    time.sleep(0.02)
            start()

        self._queues[request.target.target_id].submit(run_task)
        return pb.TaskHandle(target=request.target, task_id=task.task_id)

    def _status(self, task: _Task) -> pb.TaskStatus:
        ref = pb.TaskRef(target=pb.TargetRef(target_id=task.target_id), task_id=task.task_id)
        status = pb.TaskStatus(task=ref, state=pb.TaskStatus.State.QUEUED,
                               updated_at=_ts(task.updated_at))
        if task.failed is not None:
            status.state = pb.TaskStatus.State.FAILED
            status.error.CopyFrom(task.failed)
            return status
        if task.cancelled_before_run:
            status.state = pb.TaskStatus.State.CANCELLED
            return status
        if task.qrmi_task_id is None:
            return status

        resource = self._resources[task.target_id]
        state, err = resource.status(task.qrmi_task_id)
        if state == "QUEUED":
            status.state = pb.TaskStatus.State.QUEUED
        elif state == "RUNNING":
            status.state = pb.TaskStatus.State.RUNNING
        elif state == "SUCCEEDED":
            status.state = pb.TaskStatus.State.SUCCEEDED
            self._attach_result(status, task, resource)
        elif state == "CANCELLED":
            status.state = pb.TaskStatus.State.CANCELLED
        else:
            status.state = pb.TaskStatus.State.FAILED
            status.error.CopyFrom(pb.ErrorDetail(
                category=pb.ErrorDetail.Category.VENDOR_ERROR, retriable=True,
                vendor_code="QRMI_TASK_FAILED", vendor_message=err[:500]))
        return status

    def _attach_result(self, status: pb.TaskStatus, task: _Task, resource) -> None:
        import json

        counts = resource.result(task.qrmi_task_id)
        status.result.CopyFrom(pb.Result(
            format="counts-json",
            inline=json.dumps({"counts": dict(sorted(counts.items()))},
                              sort_keys=True).encode()))
        now = _ts(time.time())
        status.usage.append(pb.UsageRecord(unit="shots", amount=float(task.shots or 0),
                                           recorded_at=now))
        status.usage.append(pb.UsageRecord(unit="tasks", amount=1.0, recorded_at=now))

    def _get(self, request, context) -> _Task:
        self._resource(request.target.target_id, context)
        task = self._tasks.get(request.task_id)
        if task is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"task {request.task_id!r} not found")
        return task

    def GetTask(self, request, context):
        return self._status(self._get(request, context))

    def WatchTask(self, request, context):
        task = self._get(request, context)
        last = None
        while context.is_active():
            status = self._status(task)
            if status.state != last:
                last = status.state
                yield status
                if status.state in _TERMINAL:
                    return
            time.sleep(1)

    def CancelTask(self, request, context):
        task = self._get(request, context)
        with self._lock:
            if task.failed is not None or task.cancelled_before_run:
                return pb.CancelTaskResponse(accepted=False)
            task.cancel_requested = True
            qrmi_id = task.qrmi_task_id
        if qrmi_id is None:
            return pb.CancelTaskResponse(accepted=True)
        try:
            self._resources[task.target_id].stop(qrmi_id)
            return pb.CancelTaskResponse(accepted=True)
        except Exception:  # noqa: BLE001 — cancel is best-effort by contract
            return pb.CancelTaskResponse(accepted=False)

    # -- sessions (not declared) -------------------------------------------

    def OpenSession(self, request, context):
        context.abort(grpc.StatusCode.UNIMPLEMENTED, "sessions capability not declared")

    def CloseSession(self, request, context):
        context.abort(grpc.StatusCode.UNIMPLEMENTED, "sessions capability not declared")


def _validate_qasm(raw: bytes) -> str:
    """Decode + parse locally so bad programs are INVALID_PROGRAM here."""
    import base64
    import binascii

    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        try:
            text = base64.b64decode(raw, validate=True).decode("utf-8")
        except (binascii.Error, UnicodeDecodeError) as exc:
            raise _Categorized(pb.ErrorDetail.Category.INVALID_PROGRAM,
                               f"payload is neither UTF-8 QASM nor base64: {exc}") from exc
    try:
        qasm3.loads(text)
    except Exception as exc:  # noqa: BLE001 — parse failures are the program's fault
        raise _Categorized(pb.ErrorDetail.Category.INVALID_PROGRAM,
                           f"{type(exc).__name__}: {exc}"[:500]) from exc
    return text
