# SPDX-License-Identifier: Apache-2.0
"""AdapterService implementation over the task engine."""

from __future__ import annotations

import threading
import time
import uuid
from datetime import UTC, datetime

import grpc
from google.protobuf import duration_pb2, timestamp_pb2

from tangle.adapter.v1alpha1 import adapter_pb2 as pb
from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from . import tasks as taskmod
from .fleet import TargetRuntime
from .replay import ReplayClock
from .targets import TargetConfig
from .tasks import TaskEngine

VENDOR = "aer-sim"
MODALITY = "gate-model"


def nominal_2q_median(cfg: TargetConfig) -> float:
    values = sorted(m.value for m in cfg.snapshot.metrics if m.name.startswith("gate.2q."))
    if not values:
        return 0.0
    mid = len(values) // 2
    if len(values) % 2:
        return values[mid]
    return (values[mid - 1] + values[mid]) / 2
# Rough per-task service time used for best-effort wait estimates.
EST_SECONDS_PER_TASK = 1.0

_STATE_TO_PB = {
    taskmod.QUEUED: pb.TaskStatus.State.QUEUED,
    taskmod.RUNNING: pb.TaskStatus.State.RUNNING,
    taskmod.SUCCEEDED: pb.TaskStatus.State.SUCCEEDED,
    taskmod.FAILED: pb.TaskStatus.State.FAILED,
    taskmod.CANCELLED: pb.TaskStatus.State.CANCELLED,
}

_CATEGORY_TO_PB = {
    taskmod.INVALID_PROGRAM: pb.ErrorDetail.Category.INVALID_PROGRAM,
    taskmod.SESSION_LOST: pb.ErrorDetail.Category.SESSION_LOST,
    taskmod.CAPABILITY_MISMATCH: pb.ErrorDetail.Category.CAPABILITY_MISMATCH,
    taskmod.VENDOR_ERROR: pb.ErrorDetail.Category.VENDOR_ERROR,
}


def _ts(unix_seconds: float) -> timestamp_pb2.Timestamp:
    ts = timestamp_pb2.Timestamp()
    ts.FromNanoseconds(int(unix_seconds * 1e9))
    return ts


def _rfc3339_ts(text: str) -> timestamp_pb2.Timestamp:
    dt = datetime.fromisoformat(text.replace("Z", "+00:00")).astimezone(UTC)
    ts = timestamp_pb2.Timestamp()
    ts.FromDatetime(dt)
    return ts


class AdapterService(pb_grpc.AdapterServiceServicer):
    def __init__(self, targets: list[TargetConfig], clock: ReplayClock | None = None):
        self._targets = {t.target_id: t for t in targets}
        self._runtimes = {t.target_id: TargetRuntime(t, clock) for t in targets}
        self._engine = TaskEngine(self._runtimes)
        self._sessions: dict[str, dict] = {}
        self._sessions_lock = threading.Lock()

    # -- discovery ----------------------------------------------------------

    def ListTargets(self, request, context):
        return pb.ListTargetsResponse(
            targets=[self._target_info(t) for t in self._targets.values()]
        )

    def GetCapabilities(self, request, context):
        cfg = self._lookup(request.target_id, context)
        return pb.Capabilities(
            target=self._target_info(cfg),
            num_qubits=cfg.num_qubits,
            coupling_map=[pb.CouplingEdge(a=a, b=b) for a, b in cfg.coupling_map],
            native_gates=list(cfg.native_gates),
            program_formats=list(cfg.program_formats),
            max_shots=cfg.max_shots,
            sessions=True,
            cancellation=True,
            billing_units=list(cfg.billing_units),
            coupling_class="loose",
            cloud_queue=False,  # RFC-0001: replay targets are site-local
            vendor_extensions={
                "technology": cfg.technology,
                # The device's advertised baseline quality — static per
                # target, drift-blind: what static-best/v0 selects on.
                "nominal-2q-error-median": f"{nominal_2q_median(cfg):.6g}",
            },
        )

    def GetDeviceState(self, request, context):
        cfg = self._lookup(request.target_id, context)
        return self._device_state(cfg)

    def WatchDeviceState(self, request, context):
        cfg = self._lookup(request.target_id, context)
        while context.is_active():
            yield self._device_state(cfg)
            time.sleep(2.0)

    def _device_state(self, cfg: TargetConfig) -> pb.DeviceState:
        depth = self._engine.queue_depth(cfg.target_id)
        wait = duration_pb2.Duration()
        wait.FromNanoseconds(int(depth * EST_SECONDS_PER_TASK * 1e9))
        # The same snapshot the noise model consumes (T4.single-source).
        snap, measured_wall = self._runtimes[cfg.target_id].current_snapshot()
        return pb.DeviceState(
            target=pb.TargetRef(target_id=cfg.target_id),
            status=pb.DeviceState.Status.ONLINE,
            queue_depth=depth,
            estimated_wait=wait,
            unknown_queue=False,
            calibration=pb.CalibrationSnapshot(
                snapshot_id=snap.snapshot_id,
                measured_at=_ts(measured_wall),
                source=snap.source,
                metrics=[
                    pb.Metric(
                        name=m.name, value=m.value, unit=m.unit, modality=m.modality,
                        methodology=m.methodology, confidence=m.confidence, qubits=m.qubits,
                    )
                    for m in snap.metrics
                ],
            ),
            observed_at=_ts(time.time()),
        )

    def _target_info(self, cfg: TargetConfig) -> pb.TargetInfo:
        return pb.TargetInfo(
            target_id=cfg.target_id,
            display_name=cfg.display_name,
            vendor=VENDOR,
            modality=MODALITY,
            simulator=True,
            # RFC-0001 first-class field; the vendor_extensions copy stays
            # until spec v0.3 for old control planes (deprecation window).
            technology=cfg.technology,
        )

    # -- execution ----------------------------------------------------------

    def SubmitTask(self, request, context):
        cfg = self._lookup(request.target.target_id, context)
        if not request.idempotency_key:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "idempotency_key is required")
        if request.payload.WhichOneof("body") != "inline":
            context.abort(grpc.StatusCode.INVALID_ARGUMENT,
                          "this adapter accepts inline payloads only")
        precheck = None
        if request.session_id:
            precheck = self._session_error(cfg.target_id, request.session_id)
        task = self._engine.submit(
            target_id=cfg.target_id,
            idempotency_key=request.idempotency_key,
            payload_format=request.payload.format,
            program=request.payload.inline,
            shots=request.shots,
            parameters=dict(request.parameters),
            precheck_error=precheck,
        )
        return pb.TaskHandle(
            target=pb.TargetRef(target_id=cfg.target_id), task_id=task.task_id
        )

    def GetTask(self, request, context):
        task = self._get_task(request, context)
        return self._task_status(task)

    def WatchTask(self, request, context):
        self._get_task(request, context)  # existence check
        for snapshot in self._engine.watch(request.task_id):
            if not context.is_active():
                return
            yield self._task_status(snapshot)

    def CancelTask(self, request, context):
        self._get_task(request, context)
        return pb.CancelTaskResponse(accepted=self._engine.cancel(request.task_id))

    def _get_task(self, request, context):
        self._lookup(request.target.target_id, context)
        task = self._engine.get(request.task_id)
        if task is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"task {request.task_id!r} not found")
        return task

    def _task_status(self, task) -> pb.TaskStatus:
        status = pb.TaskStatus(
            task=pb.TaskRef(target=pb.TargetRef(target_id=task.target_id),
                            task_id=task.task_id),
            state=_STATE_TO_PB[task.state],
            updated_at=_ts(task.updated_at),
        )
        if task.error is not None:
            status.error.CopyFrom(pb.ErrorDetail(
                category=_CATEGORY_TO_PB[task.error.category],
                retriable=task.error.retriable,
                vendor_code=task.error.vendor_code,
                vendor_message=task.error.vendor_message,
            ))
        if task.state == taskmod.SUCCEEDED and task.result is not None:
            status.result.CopyFrom(pb.Result(
                format="counts-json",
                inline=taskmod.result_json(task),
            ))
        for u in task.usage:
            status.usage.append(pb.UsageRecord(
                unit=u["unit"], amount=u["amount"], recorded_at=_ts(task.updated_at),
            ))
        return status

    # -- sessions (declared capability, M6) ----------------------------------
    # A session is an affinity window on one target. Wall-clock expiry (the
    # sim clock accelerates calibration drift, not session lifetimes).

    _DEFAULT_SESSION_SECONDS = 3600.0

    def OpenSession(self, request, context):
        cfg = self._lookup(request.target.target_id, context)
        max_seconds = request.max_duration.ToTimedelta().total_seconds()
        if max_seconds <= 0:
            max_seconds = self._DEFAULT_SESSION_SECONDS
        session_id = str(uuid.uuid4())
        expires = time.time() + max_seconds
        with self._sessions_lock:
            self._sessions[session_id] = {
                "target": cfg.target_id, "expires": expires, "closed": False,
            }
        handle = pb.SessionHandle(
            target=pb.TargetRef(target_id=cfg.target_id), session_id=session_id
        )
        handle.expires_at.FromSeconds(int(expires))
        return handle

    def CloseSession(self, request, context):
        with self._sessions_lock:
            sess = self._sessions.get(request.session_id)
            if sess is None:
                context.abort(grpc.StatusCode.NOT_FOUND, "unknown session")
            sess["closed"] = True
        return pb.CloseSessionResponse()

    def _session_error(self, target_id: str, session_id: str):
        with self._sessions_lock:
            sess = self._sessions.get(session_id)
            if sess is None:
                return taskmod.TaskError(
                    taskmod.SESSION_LOST, True, "unknown session " + session_id
                )
            if sess["closed"]:
                return taskmod.TaskError(taskmod.SESSION_LOST, True, "session closed")
            if time.time() >= sess["expires"]:
                return taskmod.TaskError(taskmod.SESSION_LOST, True, "session expired")
            if sess["target"] != target_id:
                return taskmod.TaskError(taskmod.SESSION_LOST, True, "session bound to another target")
        return None

    # -- helpers --------------------------------------------------------------

    def _lookup(self, target_id: str, context) -> TargetConfig:
        cfg = self._targets.get(target_id)
        if cfg is None:
            context.abort(grpc.StatusCode.NOT_FOUND, f"target {target_id!r} not served here")
        return cfg
