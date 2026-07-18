# SPDX-License-Identifier: Apache-2.0
"""Target configuration and calibration snapshot loading.

A target is one simulated QPU: qubit count, coupling map, a calibration
snapshot (static JSON at M2; replayed time series from M4), and a noise
switch. Snapshots carry full Metric provenance — the scheduler must see
exactly what the physics does.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path

import yaml


@dataclass
class Metric:
    name: str
    value: float
    unit: str
    modality: str
    methodology: str
    confidence: float = 0.0
    qubits: list[int] = field(default_factory=list)


@dataclass
class Snapshot:
    snapshot_id: str
    measured_at: str  # RFC 3339
    source: str
    metrics: list[Metric]

    @staticmethod
    def load(path: Path) -> Snapshot:
        raw = json.loads(path.read_text())
        return Snapshot(
            snapshot_id=raw["snapshot_id"],
            measured_at=raw["measured_at"],
            source=raw["source"],
            metrics=[Metric(**m) for m in raw["metrics"]],
        )

    def values(self, name: str) -> dict[tuple[int, ...], float]:
        """All values for a metric name, keyed by qubit tuple."""
        return {tuple(m.qubits): m.value for m in self.metrics if m.name == name}


@dataclass
class TargetConfig:
    target_id: str
    display_name: str
    num_qubits: int
    coupling_map: list[tuple[int, int]]
    snapshot: Snapshot  # static snapshot, or the replay baseline when drift is set
    noise: bool
    max_shots: int
    seed: int
    # Device technology, exposed via Capabilities.vendor_extensions since the
    # adapter protocol has no first-class field (spec question, D-016).
    technology: str = "superconducting"
    two_qubit_gate: str = "cx"
    # Simulation device: "CPU" (default) or "GPU" (cuQuantum/cuStateVec via
    # the qiskit-aer-gpu build — the M10 GPU-backed simulator target; the
    # adapter code is identical, the device is deploy-time config, D-044).
    device: str = "CPU"
    # Drift config (see replay.DriftConfig) enables calibration replay.
    drift: dict | None = None

    program_formats = ("openqasm3",)
    billing_units = ("shots", "tasks")

    @property
    def native_gates(self) -> tuple[str, ...]:
        return ("rz", "sx", "x", self.two_qubit_gate)


def load_config(path: str | Path) -> list[TargetConfig]:
    path = Path(path)
    raw = yaml.safe_load(path.read_text())
    targets = []
    for t in raw["targets"]:
        if "baseline_file" in t:
            targets.append(_load_replay_target(path.parent, t))
            continue
        snapshot_path = path.parent / t["snapshot_file"]
        targets.append(
            TargetConfig(
                target_id=t["target_id"],
                display_name=t.get("display_name", t["target_id"]),
                num_qubits=int(t["num_qubits"]),
                coupling_map=[tuple(edge) for edge in t.get("coupling_map", [])],
                snapshot=Snapshot.load(snapshot_path),
                noise=bool(t.get("noise", True)),
                max_shots=int(t.get("max_shots", 100_000)),
                seed=int(t.get("seed", 0)),
                technology=str(t.get("technology", "superconducting")),
                two_qubit_gate=str(t.get("two_qubit_gate", "cx")),
                device=str(t.get("sim_device", "CPU")),
            )
        )
    return targets


def _load_replay_target(base: Path, t: dict) -> TargetConfig:
    """A replay target: device shape and calibration baseline come from the
    extracted snapshot file (bench/data/snapshots/*.json), drift from config."""
    baseline_path = (base / t["baseline_file"]).resolve()
    raw = json.loads(baseline_path.read_text())
    device = raw["device"]
    return TargetConfig(
        target_id=t["target_id"],
        display_name=t.get("display_name", f"{device['backend']} replay ({device['num_qubits']}q)"),
        num_qubits=int(device["num_qubits"]),
        coupling_map=[tuple(e) for e in device["coupling_map"]],
        snapshot=Snapshot.load(baseline_path),
        noise=bool(t.get("noise", True)),
        max_shots=int(t.get("max_shots", 100_000)),
        seed=int(t.get("seed", 0)),
        technology=str(device.get("technology", "superconducting")),
        device=str(device.get("sim_device", "CPU")),
        two_qubit_gate=str(device["two_qubit_gate"]),
        drift=dict(t["drift"]) if "drift" in t else None,
    )
