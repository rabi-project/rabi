# SPDX-License-Identifier: Apache-2.0
"""AdapterService over IBM Quantum backends via qiskit-ibm-runtime.

Feature-flagged and off by default (compose profile `ibm`, requires
IBM_TOKEN). The service takes backend objects (anything with the
BackendV2 interface), so tests drive it offline with fake backends in
qiskit-ibm-runtime's local mode — the same SamplerV2 code path the real
cloud uses.

Calibration metrics come from the backend's live target properties with
methodology "vendor-reported" — real calibration, no replay.

Known MVP limitation (documented): the idempotency ledger is in-memory, so
an adapter restart loses key→job mappings (the control plane's task_id keys
are stable, and IBM-side duplicates are visible in usage records).
"""

from __future__ import annotations

import threading
import time
import uuid

import grpc
from google.protobuf import duration_pb2, timestamp_pb2
from qiskit import QuantumCircuit, qasm3, transpile
from qiskit_ibm_runtime import SamplerV2
from tangle.adapter.v1alpha1 import adapter_pb2 as pb
from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

VENDOR = "ibm"
MODALITY = "gate-model"
PROGRAM_FORMATS = ("openqasm3",)
BILLING_UNITS = ("shots", "qpu-seconds")

_TERMINAL = (pb.TaskStatus.State.SUCCEEDED, pb.TaskStatus.State.FAILED,
             pb.TaskStatus.State.CANCELLED)


def _ts(seconds: float) -> timestamp_pb2.Timestamp:
    ts = timestamp_pb2.Timestamp()
    ts.FromNanoseconds(int(seconds * 1e9))
    return ts


def _metric(name, value, unit, qubits):
    return pb.Metric(name=name, value=float(value), unit=unit, modality=MODALITY,
                     methodology="vendor-reported", qubits=qubits)


def target_metrics(target) -> list[pb.Metric]:
    """Extract calibration metrics from a qiskit BackendV2 Target."""
    metrics: list[pb.Metric] = []
    twoq = next((g for g in ("cz", "ecr", "cx") if g in target.operation_names), None)
    for q in range(target.num_qubits):
        props = target.qubit_properties[q] if target.qubit_properties else None
        if props is not None:
            if getattr(props, "t1", None):
                metrics.append(_metric("t1.us", props.t1 * 1e6, "us", [q]))
            if getattr(props, "t2", None):
                metrics.append(_metric("t2.us", props.t2 * 1e6, "us", [q]))
        meas = target.get("measure", {}).get((q,)) if "measure" in target else None
        if meas is not None and meas.error is not None:
            metrics.append(_metric("readout.error", meas.error, "probability", [q]))
        sx = target.get("sx", {}).get((q,)) if "sx" in target else None
        if sx is not None and sx.error is not None:
            metrics.append(_metric("gate.1q.error", sx.error, "probability", [q]))
    if twoq:
        for qubits, inst in target[twoq].items():
            if inst is not None and inst.error is not None and len(qubits) == 2:
                metrics.append(_metric(f"gate.2q.{twoq}.error", inst.error,
                                       "probability", list(qubits)))
    return metrics


class _Task:
    def __init__(self, task_id: str, backend_name: str, shots: int):
        self.task_id = task_id
        self.backend_name = backend_name
        self.shots = shots
        self.runtime_job = None
        self.failed: pb.ErrorDetail | None = None  # pre-submission failure
        self.updated_at = time.time()


class IBMAdapterService(pb_grpc.AdapterServiceServicer):
    """Serves one or more IBM backends. `backends` maps target_id → BackendV2."""

    def __init__(self, backends: dict[str, object]):
        self._backends = backends
        self._lock = threading.Lock()
        self._tasks: dict[str, _Task] = {}
        self._by_key: dict[tuple[str, str], str] = {}

    # -- discovery ----------------------------------------------------------

    def _backend(self, target_id: str, context):
        backend = self._backends.get(target_id)
        if backend is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"target {target_id!r} not served here")
        return backend

    def _info(self, target_id: str) -> pb.TargetInfo:
        return pb.TargetInfo(target_id=target_id, display_name=target_id,
                             vendor=VENDOR, modality=MODALITY, simulator=False)

    def ListTargets(self, request, context):
        return pb.ListTargetsResponse(targets=[self._info(t) for t in self._backends])

    def GetCapabilities(self, request, context):
        backend = self._backend(request.target_id, context)
        target = backend.target
        twoq = next((g for g in ("cz", "ecr", "cx") if g in target.operation_names), "")
        coupling = []
        if twoq:
            seen = set()
            for qubits in target[twoq]:
                key = (min(qubits), max(qubits))
                if key not in seen:
                    seen.add(key)
                    coupling.append(pb.CouplingEdge(a=qubits[0], b=qubits[1]))
        return pb.Capabilities(
            target=self._info(request.target_id),
            num_qubits=target.num_qubits,
            coupling_map=coupling,
            native_gates=sorted(target.operation_names),
            program_formats=list(PROGRAM_FORMATS),
            max_shots=getattr(backend, "max_shots", 100_000) or 100_000,
            sessions=False,
            cancellation=True,
            billing_units=list(BILLING_UNITS),
            coupling_class="loose",
            cloud_queue=True,  # RFC-0001: tasks traverse IBM's shared queue
            vendor_extensions={"technology": "superconducting", "cloud": "true"},
            technology="superconducting",
        )

    def GetDeviceState(self, request, context):
        backend = self._backend(request.target_id, context)
        queue_depth, online = 0, True
        status_fn = getattr(backend, "status", None)
        if callable(status_fn):
            try:
                st = status_fn()
                queue_depth = getattr(st, "pending_jobs", 0) or 0
                online = bool(getattr(st, "operational", True))
            except Exception:  # noqa: BLE001 — status is best-effort
                pass
        metrics = target_metrics(backend.target)
        wait = duration_pb2.Duration()
        wait.FromSeconds(int(queue_depth * 60))  # crude open-plan estimate
        import hashlib
        content = ",".join(f"{m.name}:{m.qubits}:{m.value:.9g}" for m in metrics)
        snap_id = "ibm-" + hashlib.sha256(content.encode()).hexdigest()[:12]
        return pb.DeviceState(
            target=pb.TargetRef(target_id=request.target_id),
            status=pb.DeviceState.Status.ONLINE if online else pb.DeviceState.Status.OFFLINE,
            queue_depth=queue_depth,
            estimated_wait=wait,
            unknown_queue=not callable(status_fn),
            calibration=pb.CalibrationSnapshot(
                snapshot_id=snap_id, measured_at=_ts(time.time()),
                source="ibm-vendor-api", metrics=metrics),
            observed_at=_ts(time.time()),
        )

    def WatchDeviceState(self, request, context):
        while context.is_active():
            yield self.GetDeviceState(request, context)
            time.sleep(30)

    # -- execution ----------------------------------------------------------

    def SubmitTask(self, request, context):
        backend = self._backend(request.target.target_id, context)
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

        try:
            if request.payload.format not in PROGRAM_FORMATS:
                raise _Categorized(pb.ErrorDetail.Category.CAPABILITY_MISMATCH,
                                   f"format {request.payload.format!r} unsupported")
            circuit = self._parse(request.payload.inline)
            if circuit.num_qubits > backend.target.num_qubits:
                raise _Categorized(pb.ErrorDetail.Category.CAPABILITY_MISMATCH,
                                   f"needs {circuit.num_qubits} qubits, backend has "
                                   f"{backend.target.num_qubits}")
            transpiled = transpile(circuit, backend=backend, optimization_level=1,
                                   seed_transpiler=7)
            sampler = SamplerV2(mode=backend)
            job = sampler.run([transpiled], shots=request.shots or 1024)
            with self._lock:
                task.runtime_job = job
                task.updated_at = time.time()
        except _Categorized as exc:
            with self._lock:
                task.failed = exc.detail
                task.updated_at = time.time()
        except Exception as exc:  # noqa: BLE001 — map to taxonomy, never bare
            category = pb.ErrorDetail.Category.INVALID_PROGRAM \
                if isinstance(exc, (ValueError, qasm3.QASM3ImporterError)) \
                else pb.ErrorDetail.Category.VENDOR_ERROR
            with self._lock:
                task.failed = pb.ErrorDetail(
                    category=category, retriable=False,
                    vendor_code=type(exc).__name__, vendor_message=str(exc)[:500])
                task.updated_at = time.time()

        return pb.TaskHandle(target=request.target, task_id=task.task_id)

    @staticmethod
    def _parse(raw: bytes) -> QuantumCircuit:
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
        return qasm3.loads(text)

    def _status(self, task: _Task) -> pb.TaskStatus:
        ref = pb.TaskRef(target=pb.TargetRef(target_id=task.backend_name),
                         task_id=task.task_id)
        status = pb.TaskStatus(task=ref, state=pb.TaskStatus.State.QUEUED,
                               updated_at=_ts(task.updated_at))
        if task.failed is not None:
            status.state = pb.TaskStatus.State.FAILED
            status.error.CopyFrom(task.failed)
            return status
        job = task.runtime_job
        if job is None:
            return status

        name = str(getattr(job.status(), "name", job.status())).upper()
        if name in ("QUEUED", "INITIALIZING", "VALIDATING"):
            status.state = pb.TaskStatus.State.QUEUED
        elif name == "RUNNING":
            status.state = pb.TaskStatus.State.RUNNING
        elif name in ("DONE", "COMPLETED"):
            status.state = pb.TaskStatus.State.SUCCEEDED
            self._attach_result(status, task)
        elif name == "CANCELLED":
            status.state = pb.TaskStatus.State.CANCELLED
        else:  # ERROR / FAILED
            status.state = pb.TaskStatus.State.FAILED
            message = ""
            try:
                message = str(getattr(job, "error_message", lambda: "")() or "")
            except Exception:  # noqa: BLE001
                pass
            status.error.CopyFrom(pb.ErrorDetail(
                category=pb.ErrorDetail.Category.VENDOR_ERROR, retriable=True,
                vendor_code=name, vendor_message=message[:500]))
        return status

    def _attach_result(self, status: pb.TaskStatus, task: _Task) -> None:
        import json

        result = task.runtime_job.result()
        pub = result[0]
        bits = next(iter(pub.data.values()))
        counts = bits.get_counts()
        status.result.CopyFrom(pb.Result(
            format="counts-json",
            inline=json.dumps({"counts": dict(sorted(counts.items()))},
                              sort_keys=True).encode()))
        now = _ts(time.time())
        status.usage.append(pb.UsageRecord(unit="shots", amount=float(task.shots or 0),
                                           recorded_at=now))
        seconds = 0.0
        usage_fn = getattr(task.runtime_job, "usage", None)
        if callable(usage_fn):
            try:
                seconds = float(usage_fn() or 0.0)
            except Exception:  # noqa: BLE001
                seconds = 0.0
        if seconds > 0:
            status.usage.append(pb.UsageRecord(unit="qpu-seconds", amount=seconds,
                                               recorded_at=now))

    def _get(self, request, context) -> _Task:
        self._backend(request.target.target_id, context)
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
            time.sleep(2)

    def CancelTask(self, request, context):
        task = self._get(request, context)
        if task.failed is not None or task.runtime_job is None:
            return pb.CancelTaskResponse(accepted=False)
        try:
            task.runtime_job.cancel()
            return pb.CancelTaskResponse(accepted=True)
        except Exception:  # noqa: BLE001 — cancel is best-effort by contract
            return pb.CancelTaskResponse(accepted=False)

    # -- sessions (not declared) -------------------------------------------

    def OpenSession(self, request, context):
        context.abort(grpc.StatusCode.UNIMPLEMENTED, "sessions capability not declared")

    def CloseSession(self, request, context):
        context.abort(grpc.StatusCode.UNIMPLEMENTED, "sessions capability not declared")


class _Categorized(Exception):
    def __init__(self, category, message: str):
        super().__init__(message)
        self.detail = pb.ErrorDetail(category=category, retriable=False,
                                     vendor_message=message[:500])
