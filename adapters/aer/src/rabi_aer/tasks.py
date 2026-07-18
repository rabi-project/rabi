# SPDX-License-Identifier: Apache-2.0
"""Task execution engine: a per-target single-worker queue with strict
idempotency, forward-only state transitions, and watchable status.

Conformance obligations implemented here (spec conformance/README.md):
  2. states only move QUEUED→RUNNING→terminal; terminal immutable;
     timestamps monotonic
  3. re-submission with the same idempotency_key returns the same task and
     never duplicates execution or usage
  4. cancel on QUEUED prevents execution; cancel on RUNNING is best-effort
  6. usage present at terminal state; zero for never-run cancels
  7. induced failures map to ErrorDetail categories, never bare strings
"""

from __future__ import annotations

import base64
import binascii
import json
import threading
import time
import uuid
import zlib
from collections.abc import Iterator
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field

from qiskit import transpile

from .fleet import TargetRuntime
from .targets import TargetConfig

# States mirror tangle.adapter.v1alpha1.TaskStatus.State.
QUEUED = "QUEUED"
RUNNING = "RUNNING"
SUCCEEDED = "SUCCEEDED"
FAILED = "FAILED"
CANCELLED = "CANCELLED"
TERMINAL = {SUCCEEDED, FAILED, CANCELLED}

# Error categories mirror ErrorDetail.Category.
INVALID_PROGRAM = "INVALID_PROGRAM"
CAPABILITY_MISMATCH = "CAPABILITY_MISMATCH"
VENDOR_ERROR = "VENDOR_ERROR"
SESSION_LOST = "SESSION_LOST"

MAX_INLINE_BYTES = 4 * 1024 * 1024


class _CancelledDuringRun(Exception):
    """Internal signal: cancel_requested observed before simulation started."""

# Test-only knob: lets suites hold a task in the queue/running state long
# enough to exercise cancellation deterministically.
DELAY_PARAM = "rabi.sim/delay-ms"


@dataclass
class TaskError:
    category: str
    retriable: bool
    vendor_code: str = ""
    vendor_message: str = ""


@dataclass
class Task:
    task_id: str
    target_id: str
    idempotency_key: str
    payload_format: str
    program: bytes
    shots: int
    parameters: dict[str, str]
    state: str = QUEUED
    error: TaskError | None = None
    result: dict | None = None
    usage: list[dict] = field(default_factory=list)
    updated_at: float = field(default_factory=time.time)
    cancel_requested: bool = False


class TaskEngine:
    """Owns every task for one adapter process (all its targets)."""

    def __init__(self, runtimes: dict[str, TargetRuntime]):
        self._runtimes = runtimes
        self._targets: dict[str, TargetConfig] = {tid: rt.cfg for tid, rt in runtimes.items()}
        self._lock = threading.Lock()
        self._changed = threading.Condition(self._lock)
        self._tasks: dict[str, Task] = {}
        self._by_key: dict[tuple[str, str], str] = {}
        # One worker per target: an honest queue with visible depth.
        self._pools = {
            tid: ThreadPoolExecutor(max_workers=1, thread_name_prefix=f"aer-{tid}")
            for tid in runtimes
        }

    # -- submission ---------------------------------------------------------

    def submit(self, target_id: str, idempotency_key: str, payload_format: str,
               program: bytes, shots: int, parameters: dict[str, str],
               precheck_error: TaskError | None = None) -> Task:
        """Create (or return the existing) task for this idempotency key.

        precheck_error lets the service layer fail the task with a
        categorized error decided before submission (e.g. SESSION_LOST for
        a dead session) — never a bare gRPC abort, so the control plane
        sees the taxonomy (spec §errors).
        """
        cfg = self._targets[target_id]
        with self._lock:
            existing_id = self._by_key.get((target_id, idempotency_key))
            if existing_id is not None:
                return self._tasks[existing_id]

            task = Task(
                task_id=str(uuid.uuid4()),
                target_id=target_id,
                idempotency_key=idempotency_key,
                payload_format=payload_format,
                program=program,
                shots=shots,
                parameters=dict(parameters),
            )
            # Admission checks that are cheap and deterministic fail the task
            # immediately with a categorized error (never a bare string).
            err = precheck_error or self._admission_error(cfg, task)
            if err is not None:
                task.state = FAILED
                task.error = err
            self._tasks[task.task_id] = task
            self._by_key[(target_id, idempotency_key)] = task.task_id
            if task.state == QUEUED:
                self._pools[target_id].submit(self._run, task.task_id)
            self._changed.notify_all()
            return task

    @staticmethod
    def _admission_error(cfg: TargetConfig, task: Task) -> TaskError | None:
        if task.payload_format not in cfg.program_formats:
            return TaskError(
                CAPABILITY_MISMATCH, False,
                vendor_message=f"format {task.payload_format!r} not in declared "
                               f"program_formats {list(cfg.program_formats)}")
        if task.shots > cfg.max_shots:
            return TaskError(
                CAPABILITY_MISMATCH, False,
                vendor_message=f"shots {task.shots} exceed declared max_shots {cfg.max_shots}")
        if len(task.program) > MAX_INLINE_BYTES:
            return TaskError(INVALID_PROGRAM, False,
                             vendor_message="inline payload exceeds 4 MiB")
        return None

    # -- execution ----------------------------------------------------------

    def _run(self, task_id: str) -> None:
        with self._lock:
            task = self._tasks[task_id]
            if task.state != QUEUED:  # cancelled while queued: never executes
                return
            task.state = RUNNING
            self._touch(task)
            cfg = self._targets[task.target_id]
            program = task.program
            shots = task.shots
            delay_ms = int(task.parameters.get(DELAY_PARAM, "0"))

        error: TaskError | None = None
        result: dict | None = None
        try:
            circuit = self._parse(program)
            if circuit.num_qubits > cfg.num_qubits:
                error = TaskError(
                    CAPABILITY_MISMATCH, False,
                    vendor_message=f"circuit needs {circuit.num_qubits} qubits, "
                                   f"target has {cfg.num_qubits}")
            else:
                if delay_ms:
                    deadline = time.time() + delay_ms / 1000
                    while time.time() < deadline:
                        time.sleep(0.01)
                        with self._lock:
                            if self._tasks[task_id].cancel_requested:
                                break
                with self._lock:
                    if self._tasks[task_id].cancel_requested:
                        # Best-effort running-cancel honored: nothing executed,
                        # so usage stays empty and state becomes CANCELLED.
                        raise _CancelledDuringRun()
                # The snapshot in effect *now*: the replay clock may have
                # advanced since submission, and the physics must match what
                # GetDeviceState reports at execution time.
                runtime = self._runtimes[task.target_id]
                snapshot, _ = runtime.current_snapshot()
                simulator = runtime.simulator_for(snapshot)
                # initial_layout pins the identity mapping: Qiskit's seeded
                # layout search is not run-to-run deterministic (wall-clock
                # budgets inside VF2), and adapter replays must be
                # bit-identical per idempotency key (D-025).
                transpiled = transpile(
                    circuit,
                    basis_gates=list(cfg.native_gates),
                    coupling_map=[list(e) for e in cfg.coupling_map] or None,
                    initial_layout=list(range(circuit.num_qubits)),
                    optimization_level=1,
                    seed_transpiler=cfg.seed,
                )
                seed = (cfg.seed * 2654435761 + zlib.crc32(task.idempotency_key.encode())) % (2**31)
                job = simulator.run(transpiled, shots=shots, seed_simulator=seed)
                counts = job.result().get_counts()
                result = {"counts": {k.replace(" ", ""): v for k, v in counts.items()}}
        except _CancelledDuringRun:
            pass  # result and error stay None → CANCELLED below
        except Exception as exc:  # parse/build failures are the program's fault
            error = TaskError(INVALID_PROGRAM, False,
                              vendor_code=type(exc).__name__, vendor_message=str(exc)[:500])

        with self._lock:
            task = self._tasks[task_id]
            if task.state in TERMINAL:  # e.g. cancelled while running
                return
            if task.cancel_requested and result is None and error is None:
                task.state = CANCELLED
            elif error is not None:
                task.state = FAILED
                task.error = error
            else:
                task.state = SUCCEEDED
                task.result = result
                task.usage = [
                    {"unit": "shots", "amount": float(shots)},
                    {"unit": "tasks", "amount": 1.0},
                ]
            self._touch(task)
            self._changed.notify_all()

    @staticmethod
    def _parse(program: bytes):
        from qiskit import qasm3

        try:
            text = program.decode("utf-8")
        except UnicodeDecodeError:
            try:
                text = base64.b64decode(program, validate=True).decode("utf-8")
            except (binascii.Error, UnicodeDecodeError) as exc:
                raise ValueError(f"payload is neither UTF-8 QASM nor base64: {exc}") from exc
        return qasm3.loads(text)

    # -- queries ------------------------------------------------------------

    def get(self, task_id: str) -> Task | None:
        with self._lock:
            return self._tasks.get(task_id)

    def queue_depth(self, target_id: str) -> int:
        with self._lock:
            return sum(1 for t in self._tasks.values()
                       if t.target_id == target_id and t.state in (QUEUED, RUNNING))

    def watch(self, task_id: str, poll_timeout: float = 0.1) -> Iterator[Task]:
        """Yield the task now and after every state change until terminal."""
        last_state = None
        while True:
            with self._lock:
                task = self._tasks.get(task_id)
                if task is None:
                    return
                if task.state != last_state:
                    last_state = task.state
                    snapshot = _copy(task)
                else:
                    self._changed.wait(timeout=poll_timeout)
                    task = self._tasks[task_id]
                    if task.state == last_state:
                        continue
                    last_state = task.state
                    snapshot = _copy(task)
            yield snapshot
            if snapshot.state in TERMINAL:
                return

    # -- cancellation -------------------------------------------------------

    def cancel(self, task_id: str) -> bool:
        """True if the cancel was accepted (not: guaranteed)."""
        with self._lock:
            task = self._tasks.get(task_id)
            if task is None or task.state in TERMINAL:
                return False
            task.cancel_requested = True
            if task.state == QUEUED:
                # Worker will observe the state and skip execution.
                task.state = CANCELLED
                self._touch(task)
                self._changed.notify_all()
            return True

    @staticmethod
    def _touch(task: Task) -> None:
        task.updated_at = max(time.time(), task.updated_at)  # monotonic


def _copy(task: Task) -> Task:
    return Task(
        task_id=task.task_id, target_id=task.target_id,
        idempotency_key=task.idempotency_key, payload_format=task.payload_format,
        program=task.program, shots=task.shots, parameters=task.parameters,
        state=task.state, error=task.error, result=task.result,
        usage=list(task.usage), updated_at=task.updated_at,
        cancel_requested=task.cancel_requested,
    )


def result_json(task: Task) -> bytes:
    return json.dumps(task.result or {}, sort_keys=True).encode()
