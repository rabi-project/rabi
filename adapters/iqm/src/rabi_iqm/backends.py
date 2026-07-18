# SPDX-License-Identifier: Apache-2.0
"""IQM Resonance backends for the shared adapter chassis (M10).

IQM was chosen over Pasqal Cloud as the second EU vendor because Pasqal
Cloud is already reachable through the QRMI driver's resource types; a
dedicated driver would duplicate that path (docs/decisions.md D-044).
Neither vendor has credentials in this repo, so — same precedent as
QRMI/IBM — ``CassetteIqm`` is what CI certifies and ``LiveIqm`` binds the
real SDK behind the optional ``iqm`` extra for nightly/credentialed runs.
"""

from __future__ import annotations

import threading
import uuid


class LiveIqm:
    """One IQM Resonance quantum computer via qiskit-iqm."""

    def __init__(self, server_url: str, quantum_computer: str = ""):
        from iqm.qiskit_iqm import IQMProvider  # optional extra

        self._provider = IQMProvider(server_url)
        self._backend = self._provider.get_backend(quantum_computer or None)
        self._server_url = server_url
        self._jobs: dict[str, object] = {}
        self._lock = threading.Lock()

    def describe(self) -> dict:
        num_qubits = int(getattr(self._backend, "num_qubits", 0))
        return {
            "resource_id": getattr(self._backend, "name", "iqm"),
            "resource_type": "iqm-resonance",
            "technology": "superconducting",
            "cloud": True,
            "num_qubits": num_qubits,
            # IQM's calibration quality metrics ride its calibration-set
            # API; map what the SDK exposes, tagged as vendor-reported.
            "metrics": [],
            "metadata": {"server": self._server_url},
        }

    def start(self, qasm3: str, shots: int) -> str:
        from qiskit import qasm3 as qiskit_qasm3, transpile

        circuit = qiskit_qasm3.loads(qasm3)
        transpiled = transpile(circuit, backend=self._backend, optimization_level=1,
                               seed_transpiler=7)
        job = self._backend.run(transpiled, shots=shots)
        task_id = str(uuid.uuid4())
        with self._lock:
            self._jobs[task_id] = job
        return task_id

    def status(self, task_id: str) -> tuple[str, str]:
        with self._lock:
            job = self._jobs.get(task_id)
        if job is None:
            return "FAILED", "unknown IQM job"
        name = str(getattr(job.status(), "name", job.status())).upper()
        mapped = {
            "QUEUED": "QUEUED", "INITIALIZING": "QUEUED", "VALIDATING": "QUEUED",
            "RUNNING": "RUNNING", "DONE": "SUCCEEDED", "CANCELLED": "CANCELLED",
            "ERROR": "FAILED",
        }.get(name, "RUNNING")
        return mapped, ""

    def result(self, task_id: str) -> dict:
        with self._lock:
            job = self._jobs[task_id]
        counts = job.result().get_counts()
        return {str(k): int(v) for k, v in counts.items()}

    def stop(self, task_id: str) -> None:
        with self._lock:
            job = self._jobs.get(task_id)
        if job is not None:
            job.cancel()

    def close(self) -> None:
        pass


class CassetteIqm:
    """Deterministic IQM-shaped stand-in for tokenless CI certification."""

    def __init__(self, resource_id: str = "cassette-iqm"):
        self._resource_id = resource_id
        self._lock = threading.Lock()
        self._tasks: dict[str, dict] = {}

    def describe(self) -> dict:
        return {
            "resource_id": self._resource_id,
            "resource_type": "iqm-resonance",
            "technology": "superconducting",
            "cloud": True,
            "num_qubits": 20,
            "metrics": [
                {"name": "gate.2q.cz.error", "value": 0.0058, "qubits": [0, 1],
                 "methodology": "vendor-reported", "upstream": "cassette-2026-07"},
                {"name": "gate.2q.cz.error", "value": 0.0091, "qubits": [1, 2],
                 "methodology": "vendor-reported", "upstream": "cassette-2026-07"},
                {"name": "readout.error", "value": 0.018, "qubits": [0],
                 "methodology": "vendor-reported", "upstream": "cassette-2026-07"},
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
