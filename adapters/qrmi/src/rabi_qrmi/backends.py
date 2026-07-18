# SPDX-License-Identifier: Apache-2.0
"""QRMI backend abstraction.

The adapter speaks to one of two implementations of the same small
interface:

- ``LiveQrmi``: wraps ``qrmi.QuantumResource`` (Python bindings — chosen
  over the C/Rust bindings to match the rest of the adapter fleet's uv
  toolchain, docs/decisions.md D-042). Needs the ``qrmi`` extra and the
  provider credentials QRMI reads from its dotenv convention.
- ``CassetteQrmi``: a deterministic, tokenless stand-in shaped like a QRMI
  resource, used by CI conformance runs. Reports label it explicitly.

Both expose: ``describe() -> dict`` (identity + calibration-ish metrics),
``start(qasm3, shots) -> task_id``, ``status(task_id) -> (state, error)``,
``result(task_id) -> counts dict``, ``stop(task_id)``.
"""

from __future__ import annotations

import json
import threading
import uuid

# Technology / cloud mapping per QRMI resource type (spec §2a registry).
RESOURCE_TECH = {
    "IBMQuantumSystem": ("superconducting", True),
    "IBMQiskitRuntimeService": ("superconducting", True),
    "PasqalCloud": ("neutral-atom", True),
    "PasqalLocal": ("neutral-atom", False),
    "IQMServer": ("superconducting", True),
    "AliceBobFelis": ("superconducting", True),
}


class LiveQrmi:
    """One QRMI-managed resource over the real bindings."""

    def __init__(self, resource_id: str, resource_type: str):
        import qrmi  # the optional extra; import here so cassette mode never needs it

        self._qrmi = qrmi
        self._res = qrmi.QuantumResource(resource_id, getattr(qrmi.ResourceType, resource_type))
        self._resource_id = resource_id
        self._resource_type = resource_type
        self._token = self._res.acquire()

    def describe(self) -> dict:
        tech, cloud = RESOURCE_TECH.get(self._resource_type, ("superconducting", True))
        meta = {}
        try:
            meta = dict(self._res.metadata())
        except Exception:  # noqa: BLE001 — metadata is advisory
            meta = {}
        target_raw = self._res.target().value
        num_qubits, metrics = _metrics_from_target(target_raw)
        return {
            "resource_id": self._resource_id,
            "resource_type": self._resource_type,
            "technology": tech,
            "cloud": cloud,
            "num_qubits": num_qubits,
            "metrics": metrics,  # each: name/value/qubits/methodology/upstream
            "metadata": meta,
        }

    def start(self, qasm3: str, shots: int) -> str:
        payload = self._qrmi.Payload.QiskitPrimitive(
            input=json.dumps(
                {"pubs": [[qasm3]], "shots": shots, "version": 2, "support_qiskit": False}
            ),
            program_id="sampler",
        )
        return self._res.task_start(payload)

    def status(self, task_id: str) -> tuple[str, str]:
        st = self._res.task_status(task_id)
        name = str(st).rsplit(".", 1)[-1]
        mapped = {
            "Queued": "QUEUED",
            "Running": "RUNNING",
            "Completed": "SUCCEEDED",
            "Failed": "FAILED",
            "Cancelled": "CANCELLED",
        }.get(name, "RUNNING")
        err = ""
        if mapped == "FAILED":
            try:
                err = str(self._res.task_logs(task_id))[-500:]
            except Exception:  # noqa: BLE001
                err = ""
        return mapped, err

    def result(self, task_id: str) -> dict:
        raw = json.loads(self._res.task_result(task_id).value)
        return _counts_from_primitive_result(raw)

    def stop(self, task_id: str) -> None:
        self._res.task_stop(task_id)

    def close(self) -> None:
        try:
            self._res.release(self._token)
        except Exception:  # noqa: BLE001 — release is best-effort on shutdown
            pass


def _metrics_from_target(raw: str) -> tuple[int, list[dict]]:
    """Map a QRMI Target document into provenance-complete metric dicts."""
    try:
        doc = json.loads(raw)
    except (TypeError, ValueError):
        return 0, []
    num_qubits = int(doc.get("num_qubits") or doc.get("n_qubits") or 0)
    metrics: list[dict] = []
    upstream = str(doc.get("backend_version") or doc.get("version") or "unknown")
    for gate in doc.get("gates", []) or []:
        props = gate.get("parameters") or []
        name = str(gate.get("gate") or gate.get("name") or "")
        for p in props:
            if p.get("name") == "gate_error" and name:
                arity = len(gate.get("qubits", [[]])[0]) if gate.get("qubits") else 2
                metric = f"gate.{arity}q.{name}.error"
                metrics.append({
                    "name": metric, "value": float(p.get("value", 0.0)),
                    "qubits": [int(q) for q in (gate.get("qubits") or [[]])[0]],
                    "methodology": "qrmi-relayed", "upstream": upstream,
                })
    return num_qubits, metrics


def _counts_from_primitive_result(raw: dict) -> dict:
    """Extract a counts histogram from a SamplerV2-shaped result document."""
    try:
        pub = raw["results"][0]
        data = pub["data"]
        field = next(iter(data.values()))
        counts = field.get("counts") or field.get("get_counts") or {}
        return {str(k): int(v) for k, v in counts.items()}
    except (KeyError, IndexError, StopIteration, TypeError):
        return {}


class CassetteQrmi:
    """Deterministic QRMI-shaped resource for tokenless CI certification.

    Behaves like a well-run cloud resource: tasks complete with an ideal
    Bell histogram, stop() cancels queued/running work, and the calibration
    metrics are fixed plausible values labeled methodology "qrmi-relayed"
    with a synthetic upstream tag. It intentionally implements the same
    interface as LiveQrmi so the adapter layer cannot tell them apart.
    """

    def __init__(self, resource_id: str, resource_type: str = "IBMQiskitRuntimeService"):
        self._resource_id = resource_id
        self._resource_type = resource_type
        self._lock = threading.Lock()
        self._tasks: dict[str, dict] = {}

    def describe(self) -> dict:
        tech, cloud = RESOURCE_TECH.get(self._resource_type, ("superconducting", True))
        return {
            "resource_id": self._resource_id,
            "resource_type": self._resource_type,
            "technology": tech,
            "cloud": cloud,
            "num_qubits": 27,
            "metrics": [
                {"name": "gate.2q.cx.error", "value": 0.0072, "qubits": [0, 1],
                 "methodology": "qrmi-relayed", "upstream": "cassette-2026-07"},
                {"name": "gate.2q.cx.error", "value": 0.0114, "qubits": [1, 2],
                 "methodology": "qrmi-relayed", "upstream": "cassette-2026-07"},
                {"name": "readout.error", "value": 0.021, "qubits": [0],
                 "methodology": "qrmi-relayed", "upstream": "cassette-2026-07"},
            ],
            "metadata": {"mode": "cassette"},
        }

    def start(self, qasm3: str, shots: int) -> str:
        task_id = str(uuid.uuid4())
        with self._lock:
            self._tasks[task_id] = {"state": "SUCCEEDED", "shots": shots}
        return task_id

    def status(self, task_id: str) -> tuple[str, str]:
        with self._lock:
            t = self._tasks.get(task_id)
            if t is None:
                return "FAILED", "unknown task"
            return t["state"], ""

    def result(self, task_id: str) -> dict:
        with self._lock:
            shots = self._tasks[task_id]["shots"]
        return {"00": shots // 2, "11": shots - shots // 2}

    def stop(self, task_id: str) -> None:
        with self._lock:
            t = self._tasks.get(task_id)
            if t is not None and t["state"] in ("QUEUED", "RUNNING"):
                t["state"] = "CANCELLED"

    def close(self) -> None:
        pass
