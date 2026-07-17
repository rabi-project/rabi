# SPDX-License-Identifier: Apache-2.0
"""Physics batch executor: measure every unique (circuit, target, snapshot)
execution context on Aer with the noise model built from the same snapshot
the scheduler saw (shared series file), against the exact ideal distribution.

Fidelity proxy: F = 1 − TVD(empirical distribution, ideal probabilities),
measured at a fixed PROBE_SHOTS regardless of the job's nominal shots. The
vendored circuit subset has analytically concentrated ideal outputs, so the
TVD sampling bias is small and identical across policies (disclosed in the
report methodology). Jobs that share a physical context share one seeded
measurement — the same experiment has the same outcome.

Simulation method: matrix_product_state for wide low-entanglement families
(ghz, wstate, bv, dj at >= 14 qubits), statevector otherwise.

Deterministic: seeds are pure functions of the context key; workers only
parallelize independent runs and results are re-sorted before writing.

Usage: uv run python scripts/execute.py --out out [--workers N]
"""

from __future__ import annotations

import argparse
import csv
import glob
import json
from concurrent.futures import ProcessPoolExecutor
from functools import lru_cache
from pathlib import Path

BENCH = Path(__file__).parent.parent
PROBE_SHOTS = 1000
MPS_FAMILIES = ("ghz", "wstate", "bv", "dj")
MPS_MIN_QUBITS = 14

_series_cache: dict | None = None


def load_series(path: Path) -> dict:
    global _series_cache
    if _series_cache is None:
        with path.open() as fh:
            raw = json.load(fh)
        _series_cache = {t["name"]: t for t in raw["targets"]}
    return _series_cache


@lru_cache(maxsize=64)
def ideal_probs(circuit_name: str):
    """Exact ideal distribution over the *measured* clbits, keyed in the same
    bit order as Aer counts strings (clbit 0 rightmost). Unmeasured qubits
    are marginalized out; unmeasured clbits read as constant 0."""
    from qiskit import qasm3
    from qiskit.quantum_info import Statevector

    src = (BENCH / "circuits" / f"{circuit_name}.qasm").read_text()
    qc = qasm3.loads(src)
    num_cl = qc.num_clbits

    meas = []  # (clbit index, qubit index), final measurement wins per clbit
    mapping = {}
    for inst in qc.data:
        if inst.operation.name == "measure":
            q = qc.find_bit(inst.qubits[0]).index
            c = qc.find_bit(inst.clbits[0]).index
            mapping[c] = q
    meas = sorted(mapping.items())  # by clbit index

    bare = qc.copy()
    bare.remove_final_measurements()
    state = Statevector.from_instruction(bare)
    qargs = [q for _, q in meas]
    marg = state.probabilities_dict(qargs)  # bit i of key (from right) = qargs[i]

    out = {}
    for key, p in marg.items():
        bits = ["0"] * num_cl
        for pos, (clbit, _) in enumerate(meas):
            bits[num_cl - 1 - clbit] = key[len(key) - 1 - pos]
        out["".join(bits)] = out.get("".join(bits), 0.0) + p
    return out


@lru_cache(maxsize=48)
def simulator_for(target_name: str, snapshot_id: str, width: int, family: str,
                  series_path_str: str):
    from qiskit_aer import AerSimulator

    from rabi_aer.noise import build_noise_model
    from rabi_aer.targets import Metric, Snapshot, TargetConfig

    series = load_series(Path(series_path_str))
    t = series[target_name]
    snap_raw = next(s for s in t["snapshots"] if s["snapshot_id"] == snapshot_id)
    snapshot = Snapshot(
        snapshot_id=snapshot_id, measured_at="2026-07-01T00:00:00Z",
        source="bench-series",
        metrics=[Metric(name=m["name"], value=m["value"], unit="", modality="gate-model",
                        methodology="replayed-vendor-calibration", qubits=m["qubits"])
                 for m in snap_raw["metrics"]],
    )
    cfg = TargetConfig(
        target_id=t["target_id"], display_name=t["name"], num_qubits=t["qubits"],
        coupling_map=[tuple(e) for e in t["coupling_map"]], snapshot=snapshot,
        noise=True, max_shots=t["max_shots"], seed=0,
        two_qubit_gate=t["two_qubit_gate"],
    )
    model = build_noise_model(cfg, snapshot)
    # Explicit methods only: "automatic" picks a method from free memory at
    # runtime, which is not deterministic across runs (T6.det). Aer's
    # multithreaded sampling is deterministic under a fixed seed (verified).
    method = ("matrix_product_state"
              if width >= MPS_MIN_QUBITS and family in MPS_FAMILIES else "statevector")
    return AerSimulator(noise_model=model, method=method), cfg


def run_one(args: tuple) -> dict:
    circuit_name, target_name, snapshot_id, phys_seed, series_path_str = args
    from qiskit import qasm3, transpile

    family = circuit_name.rsplit("_", 1)[0]
    qc = qasm3.loads((BENCH / "circuits" / f"{circuit_name}.qasm").read_text())
    simulator, cfg = simulator_for(target_name, snapshot_id, qc.num_qubits, family,
                                   series_path_str)
    # initial_layout pins the identity mapping: Qiskit's layout search
    # (VF2 with a wall-clock budget) is not run-to-run deterministic even
    # when seeded, and T6.det demands byte-identical reruns. The identity
    # layout lands on the BFS-ordered subgraph and is the same for every
    # policy, so rankings are unaffected (D-025).
    transpiled = transpile(
        qc, basis_gates=list(cfg.native_gates),
        coupling_map=[list(e) for e in cfg.coupling_map],
        initial_layout=list(range(qc.num_qubits)),
        optimization_level=1, seed_transpiler=cfg.seed,
    )
    counts = simulator.run(transpiled, shots=PROBE_SHOTS,
                           seed_simulator=phys_seed).result().get_counts()

    ideal = ideal_probs(circuit_name)
    keys = set(counts) | set(ideal)
    tvd = 0.5 * sum(abs(counts.get(k, 0) / PROBE_SHOTS - ideal.get(k, 0.0)) for k in keys)
    return {
        "circuit": circuit_name, "target": target_name, "snapshot_id": snapshot_id,
        "phys_seed": phys_seed, "fidelity": round(1.0 - tvd, 9),
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default="out")
    parser.add_argument("--workers", type=int, default=4)
    args = parser.parse_args()
    out_dir = BENCH / args.out
    series_path = out_dir / "series.json"

    tasks = {}
    for sched in sorted(glob.glob(str(out_dir / "schedule_*.json"))):
        with open(sched) as fh:
            data = json.load(fh)
        for e in data["executions"]:
            if e["unplaced"]:
                continue
            key = (e["circuit"], e["target"], e["snapshot_id"], e["phys_seed"])
            tasks[key] = key + (str(series_path),)

    todo = [tasks[k] for k in sorted(tasks)]
    print(f"{len(todo)} unique physics executions (probe shots: {PROBE_SHOTS})")

    if args.workers > 1:
        with ProcessPoolExecutor(max_workers=args.workers) as pool:
            rows = list(pool.map(run_one, todo, chunksize=4))
    else:
        rows = [run_one(t) for t in todo]

    rows.sort(key=lambda r: (r["circuit"], r["target"], r["snapshot_id"]))
    with (out_dir / "physics.csv").open("w", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=["circuit", "target", "snapshot_id",
                                           "phys_seed", "fidelity"])
        w.writeheader()
        w.writerows(rows)
    print(f"wrote {out_dir / 'physics.csv'} ({len(rows)} rows)")


if __name__ == "__main__":
    main()
