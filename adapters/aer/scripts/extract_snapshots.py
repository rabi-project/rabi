# SPDX-License-Identifier: Apache-2.0
"""Extract real device calibration snapshots from qiskit-ibm-runtime fake
backends (which ship historical calibration data offline) into versioned JSON
under bench/data/snapshots/.

Each snapshot is a connected 20-qubit subgraph of the real device (Aer cannot
simulate 127+ qubits with noise), with calibration values taken verbatim from
the fake backend's data for those physical qubits. Provenance — backend,
package version, qubit mapping, extraction command — is embedded in the file.

Usage: uv run --extra dev python scripts/extract_snapshots.py
"""

from __future__ import annotations

import json
from collections import deque
from pathlib import Path

import qiskit_ibm_runtime
from qiskit_ibm_runtime.fake_provider import FakeBrisbane, FakeSherbrooke, FakeTorino

OUT_DIR = Path(__file__).parent.parent.parent.parent / "bench" / "data" / "snapshots"
SUBGRAPH_SIZE = 20
MEASURED_AT = "2026-07-01T00:00:00Z"  # sim-world calibration epoch (replay overrides)

BACKENDS = [
    (FakeTorino, "cz"),
    (FakeSherbrooke, "ecr"),
    (FakeBrisbane, "ecr"),
]


def connected_subgraph(coupling: list[tuple[int, int]], size: int) -> list[int]:
    """BFS from qubit 0 over the coupling map: a connected physical subset."""
    adj: dict[int, set[int]] = {}
    for a, b in coupling:
        adj.setdefault(a, set()).add(b)
        adj.setdefault(b, set()).add(a)
    seen, order, queue = {0}, [], deque([0])
    while queue and len(order) < size:
        q = queue.popleft()
        order.append(q)
        for nb in sorted(adj.get(q, ())):
            if nb not in seen:
                seen.add(nb)
                queue.append(nb)
    if len(order) < size:
        raise SystemExit(f"device graph too small: got {len(order)} < {size}")
    return sorted(order)


def extract(backend_cls, twoq_gate: str) -> dict:
    backend = backend_cls()
    target = backend.target
    coupling = [tuple(e) for e in target.build_coupling_map().get_edges()]
    physical = connected_subgraph(coupling, SUBGRAPH_SIZE)
    logical = {p: i for i, p in enumerate(physical)}  # physical → 0..19

    def metric(name, value, unit, qubits):
        return {
            "name": name, "value": float(value), "unit": unit,
            "modality": "gate-model", "methodology": "vendor-reported",
            "confidence": 0.0, "qubits": qubits,
        }

    metrics = []
    for p in physical:
        li = logical[p]
        props = target.qubit_properties[p]
        if props.t1:
            metrics.append(metric("t1.us", props.t1 * 1e6, "us", [li]))
        if props.t2:
            metrics.append(metric("t2.us", props.t2 * 1e6, "us", [li]))
        meas = target["measure"].get((p,))
        if meas and meas.error is not None:
            metrics.append(metric("readout.error", meas.error, "probability", [li]))
        sx = target["sx"].get((p,))
        if sx and sx.error is not None:
            metrics.append(metric("gate.1q.error", sx.error, "probability", [li]))

    edges = []
    for a, b in coupling:
        if a in logical and b in logical:
            inst = target[twoq_gate].get((a, b))
            if inst and inst.error is not None:
                la, lb = logical[a], logical[b]
                metrics.append(metric(f"gate.2q.{twoq_gate}.error", inst.error,
                                      "probability", [la, lb]))
                edges.append([la, lb])

    # Deduplicate reversed edges (Aer treats the coupling map as undirected).
    seen, coupling_out = set(), []
    for a, b in edges:
        key = (min(a, b), max(a, b))
        if key not in seen:
            seen.add(key)
            coupling_out.append([a, b])

    return {
        "snapshot_id": f"baseline-{backend.name}-20q-v1",
        "measured_at": MEASURED_AT,
        "source": "qiskit-ibm-runtime fake backend (real historical calibration)",
        "metrics": metrics,
        "device": {
            "backend": backend.name,
            "num_qubits": SUBGRAPH_SIZE,
            "coupling_map": coupling_out,
            "two_qubit_gate": twoq_gate,
            "technology": "superconducting",
        },
        "provenance": {
            "package": "qiskit-ibm-runtime",
            "package_version": qiskit_ibm_runtime.__version__,
            "extraction_script": "adapters/aer/scripts/extract_snapshots.py",
            "physical_qubits": physical,
            "note": ("connected 20-qubit BFS subgraph of the real device; "
                     "calibration values are the vendor-reported data for those "
                     "physical qubits, remapped to logical indices 0..19"),
        },
    }


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for backend_cls, twoq in BACKENDS:
        snap = extract(backend_cls, twoq)
        out = OUT_DIR / f"{snap['device']['backend']}_20q.json"
        out.write_text(json.dumps(snap, indent=2, sort_keys=True) + "\n")
        n2q = sum(1 for m in snap["metrics"] if m["name"].startswith("gate.2q"))
        print(f"{out.name}: {len(snap['metrics'])} metrics ({n2q} two-qubit edges)")


if __name__ == "__main__":
    main()
