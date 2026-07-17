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
    snapshot: Snapshot
    noise: bool
    max_shots: int
    seed: int
    # Device technology, exposed via Capabilities.vendor_extensions since the
    # adapter protocol has no first-class field (spec question, D-016).
    technology: str = "superconducting"

    program_formats = ("openqasm3",)
    billing_units = ("shots", "tasks")
    native_gates = ("cx", "sx", "x", "rz")


def load_config(path: str | Path) -> list[TargetConfig]:
    path = Path(path)
    raw = yaml.safe_load(path.read_text())
    targets = []
    for t in raw["targets"]:
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
            )
        )
    return targets
