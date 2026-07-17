# SPDX-License-Identifier: Apache-2.0
"""Export the replay fleet's calibration snapshot series to JSON.

Both the Go benchmark runner (scheduling decisions) and the Python physics
executor consume this one file, so the scheduler and the noise model can
never diverge (single-source, T4). Deterministic: the series is a pure
function of the replay config seeds.

Usage: uv run python scripts/gen_series.py --hours 60 --out out/series.json
"""

from __future__ import annotations

import argparse
import json
from datetime import timedelta
from pathlib import Path

from rabi_aer.fleet import TargetRuntime, parse_rfc3339
from rabi_aer.service import nominal_2q_median
from rabi_aer.targets import load_config

REPLAY_CONFIG = Path(__file__).parent.parent.parent / "adapters" / "aer" / "config" / "replay.yaml"


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--hours", type=float, default=60.0)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()

    targets = []
    for cfg in load_config(REPLAY_CONFIG):
        rt = TargetRuntime(cfg)
        epoch = parse_rfc3339(cfg.snapshot.measured_at)
        step_s = float(cfg.drift["step_minutes"]) * 60.0
        steps = int(args.hours * 3600 // step_s) + 1

        snapshots = []
        last_id = None
        for k in range(steps):
            offset = k * step_s
            snap = rt.snapshot_at_sim(epoch + timedelta(seconds=offset))
            if snap.snapshot_id == last_id:
                continue  # unchanged within a drift step
            last_id = snap.snapshot_id
            snapshots.append({
                "snapshot_id": snap.snapshot_id,
                "sim_offset_s": offset,
                "metrics": [
                    {"name": m.name, "value": m.value, "qubits": m.qubits}
                    for m in snap.metrics
                ],
            })

        targets.append({
            "name": f"sim/{cfg.target_id}",
            "target_id": cfg.target_id,
            "qubits": cfg.num_qubits,
            "formats": list(cfg.program_formats),
            "billing": list(cfg.billing_units),
            "max_shots": cfg.max_shots,
            "technology": cfg.technology,
            "two_qubit_gate": cfg.two_qubit_gate,
            "coupling_map": [list(e) for e in cfg.coupling_map],
            "nominal_2q_error_median": nominal_2q_median(cfg),
            "step_seconds": step_s,
            "snapshots": snapshots,
        })

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    with out.open("w") as fh:
        json.dump({"horizon_hours": args.hours, "targets": targets}, fh,
                  indent=1, sort_keys=True)
        fh.write("\n")
    print(f"wrote {out} ({len(targets)} targets, "
          f"{sum(len(t['snapshots']) for t in targets)} snapshots)")


if __name__ == "__main__":
    main()
