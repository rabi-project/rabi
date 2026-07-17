# SPDX-License-Identifier: Apache-2.0
"""Benchmark analysis: join schedules with physics, compute per-policy
metrics with bootstrap CIs and effect sizes, emit CSVs + charts + report.md.

Deterministic: bootstrap uses a fixed-seed generator; CSV floats have fixed
precision; row order is fully specified. `make bench` twice must produce
byte-identical CSVs (T6.det).

Usage: uv run python scripts/analyze.py --out out
"""

from __future__ import annotations

import argparse
import csv
import glob
import json
from pathlib import Path

import numpy as np

BENCH = Path(__file__).parent.parent
BOOT_N = 10_000
BOOT_SEED = 20260717

POLICY_ORDER = ["calib-aware/v0", "static-best/v0", "round-robin/v0"]


def load_jobs(out_dir: Path) -> list[dict]:
    physics = {}
    with (out_dir / "physics.csv").open() as fh:
        for r in csv.DictReader(fh):
            physics[(r["circuit"], r["target"], r["snapshot_id"])] = float(r["fidelity"])

    jobs = []
    for sched in sorted(glob.glob(str(out_dir / "schedule_*.json"))):
        with open(sched) as fh:
            data = json.load(fh)
        for e in data["executions"]:
            row = dict(e)
            row["policy"] = data["policy"]
            row["seed"] = data["seed"]
            if not e["unplaced"]:
                row["fidelity"] = physics[(e["circuit"], e["target"], e["snapshot_id"])]
            else:
                row["fidelity"] = None
            jobs.append(row)
    jobs.sort(key=lambda r: (r["policy"], r["seed"], r["job_id"]))
    return jobs


def bootstrap_ci(values: np.ndarray, rng: np.random.Generator) -> tuple[float, float]:
    if len(values) == 0:
        return (float("nan"), float("nan"))
    idx = rng.integers(0, len(values), size=(BOOT_N, len(values)))
    means = values[idx].mean(axis=1)
    return (float(np.percentile(means, 2.5)), float(np.percentile(means, 97.5)))


def cohens_d(a: np.ndarray, b: np.ndarray) -> float:
    if len(a) < 2 or len(b) < 2:
        return float("nan")
    pooled = np.sqrt(((len(a) - 1) * a.var(ddof=1) + (len(b) - 1) * b.var(ddof=1))
                     / (len(a) + len(b) - 2))
    if pooled == 0:
        return float("nan")
    return float((a.mean() - b.mean()) / pooled)


def summarize(jobs: list[dict]) -> tuple[list[dict], list[dict]]:
    rng = np.random.default_rng(BOOT_SEED)
    summary, per_seed = [], []
    by_policy: dict[str, dict[str, np.ndarray]] = {}

    for policy in POLICY_ORDER:
        rows = [r for r in jobs if r["policy"] == policy]
        placed = [r for r in rows if not r["unplaced"]]
        fid = np.array([r["fidelity"] for r in placed])
        wait = np.array([r["wait_s"] for r in placed])
        floored = [r for r in placed if r["floor_2q"] > 0 or r["floor_ro"] > 0]
        viol = np.array([1.0 if r["slo_violated"] else 0.0 for r in floored])
        deadl = [r for r in placed if r["deadline_s"] > 0]
        dmet = np.array([1.0 if r["deadline_met"] else 0.0 for r in deadl])
        by_policy[policy] = {"fid": fid, "wait": wait, "viol": viol}

        fid_ci = bootstrap_ci(fid, rng)
        wait_ci = bootstrap_ci(wait, rng)
        viol_ci = bootstrap_ci(viol, rng)
        summary.append({
            "policy": policy,
            "jobs": len(rows),
            "placed": len(placed),
            "unplaced": len(rows) - len(placed),
            "mean_fidelity": f"{fid.mean():.6f}",
            "fidelity_ci_lo": f"{fid_ci[0]:.6f}",
            "fidelity_ci_hi": f"{fid_ci[1]:.6f}",
            "slo_jobs": len(floored),
            "slo_violation_rate": f"{viol.mean():.6f}" if len(viol) else "",
            "slo_ci_lo": f"{viol_ci[0]:.6f}" if len(viol) else "",
            "slo_ci_hi": f"{viol_ci[1]:.6f}" if len(viol) else "",
            "mean_wait_s": f"{wait.mean():.3f}",
            "wait_ci_lo": f"{wait_ci[0]:.3f}",
            "wait_ci_hi": f"{wait_ci[1]:.3f}",
            "median_wait_s": f"{np.median(wait):.3f}" if len(wait) else "",
            # Wait for jobs without quality floors: separates queueing cost
            # from the deliberate wait-for-calibration behavior.
            "mean_wait_nofloor_s": f"{np.array([r['wait_s'] for r in placed if r['floor_2q'] == 0 and r['floor_ro'] == 0]).mean():.3f}",
            "deadline_jobs": len(deadl),
            "deadline_met_rate": f"{dmet.mean():.6f}" if len(dmet) else "",
        })

        for seed in sorted({r["seed"] for r in rows}):
            srows = [r for r in placed if r["seed"] == seed]
            sfid = np.array([r["fidelity"] for r in srows])
            swait = np.array([r["wait_s"] for r in srows])
            sfl = [r for r in srows if r["floor_2q"] > 0 or r["floor_ro"] > 0]
            sviol = np.array([1.0 if r["slo_violated"] else 0.0 for r in sfl])
            per_seed.append({
                "policy": policy, "seed": seed, "placed": len(srows),
                "mean_fidelity": f"{sfid.mean():.6f}",
                "slo_violation_rate": f"{sviol.mean():.6f}" if len(sviol) else "",
                "mean_wait_s": f"{swait.mean():.3f}",
            })

    # Effect sizes: calib-aware vs each baseline.
    effects = []
    ca = by_policy[POLICY_ORDER[0]]
    for baseline in POLICY_ORDER[1:]:
        b = by_policy[baseline]
        effects.append({
            "comparison": f"calib-aware/v0 vs {baseline}",
            "fidelity_delta": f"{ca['fid'].mean() - b['fid'].mean():.6f}",
            "fidelity_cohens_d": f"{cohens_d(ca['fid'], b['fid']):.4f}",
            "slo_rate_delta": f"{ca['viol'].mean() - b['viol'].mean():.6f}",
            "wait_delta_s": f"{ca['wait'].mean() - b['wait'].mean():.3f}",
            "wait_cohens_d": f"{cohens_d(ca['wait'], b['wait']):.4f}",
        })
    return summary, per_seed, effects


def write_csv(path: Path, rows: list[dict]) -> None:
    with path.open("w", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)


def charts(out_dir: Path, summary: list[dict]) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    policies = [s["policy"].removesuffix("/v0") for s in summary]

    def bar(metric, lo, hi, title, fname, fmt=float):
        vals = [fmt(s[metric]) for s in summary]
        err_lo = [fmt(s[metric]) - fmt(s[lo]) for s in summary]
        err_hi = [fmt(s[hi]) - fmt(s[metric]) for s in summary]
        fig, ax = plt.subplots(figsize=(5.2, 3.4))
        ax.bar(policies, vals, yerr=[err_lo, err_hi], capsize=5,
               color=["#2a7", "#a55", "#579"])
        ax.set_title(title)
        ax.grid(axis="y", alpha=0.3)
        fig.tight_layout()
        fig.savefig(out_dir / fname, dpi=130)
        plt.close(fig)

    bar("mean_fidelity", "fidelity_ci_lo", "fidelity_ci_hi",
        "Mean result fidelity vs ideal (95% CI)", "fidelity.png")
    bar("slo_violation_rate", "slo_ci_lo", "slo_ci_hi",
        "Quality-SLO violation rate (95% CI)", "slo.png")
    bar("mean_wait_s", "wait_ci_lo", "wait_ci_hi",
        "Mean wait time, sim-seconds (95% CI)", "wait.png")


def esp_scatter(out_dir: Path, jobs: list[dict]) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    pts = [(r["esp_predicted"], r["fidelity"]) for r in jobs
           if r["policy"] == "calib-aware/v0" and not r["unplaced"]
           and r["esp_predicted"] > 0]
    if not pts:
        return
    xs, ys = zip(*pts)
    fig, ax = plt.subplots(figsize=(4.6, 4.2))
    ax.scatter(xs, ys, s=6, alpha=0.25, color="#2a7", edgecolors="none")
    ax.plot([0, 1], [0, 1], "--", color="#888", linewidth=1)
    ax.set_xlabel("predicted ESP")
    ax.set_ylabel("measured fidelity (1 − TVD)")
    ax.set_title("ESP estimator vs measured fidelity (calib-aware)")
    ax.set_xlim(0, 1)
    ax.set_ylim(0, 1)
    fig.tight_layout()
    fig.savefig(out_dir / "esp_calibration.png", dpi=130)
    plt.close(fig)


def render_report(out_dir: Path, summary, per_seed, effects, jobs) -> None:
    template = (BENCH / "report_template.md").read_text()

    def table(rows, cols):
        head = "| " + " | ".join(cols) + " |\n"
        head += "|" + "|".join("---" for _ in cols) + "|\n"
        body = "".join("| " + " | ".join(str(r[c]) for c in cols) + " |\n" for r in rows)
        return head + body

    n_jobs = len({r["job_id"] for r in jobs})  # per-seed workload size
    seeds = sorted({r["seed"] for r in jobs})
    report = template.format(
        summary_table=table(summary, ["policy", "placed", "unplaced", "mean_fidelity",
                                      "fidelity_ci_lo", "fidelity_ci_hi",
                                      "slo_violation_rate", "mean_wait_s",
                                      "deadline_met_rate"]),
        wait_table=table(summary, ["policy", "mean_wait_s", "median_wait_s",
                                   "mean_wait_nofloor_s"]),
        effects_table=table(effects, ["comparison", "fidelity_delta", "fidelity_cohens_d",
                                      "slo_rate_delta", "wait_delta_s", "wait_cohens_d"]),
        per_seed_table=table(per_seed, ["policy", "seed", "placed", "mean_fidelity",
                                        "slo_violation_rate", "mean_wait_s"]),
        n_seeds=len(seeds),
        n_jobs_per_seed=n_jobs,
        n_total=len(jobs),
        seeds=", ".join(str(s) for s in seeds),
    )
    (out_dir / "report.md").write_text(report)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default="out")
    parser.add_argument("--no-charts", action="store_true")
    args = parser.parse_args()
    out_dir = BENCH / args.out

    jobs = load_jobs(out_dir)
    result_cols = ["policy", "seed", "job_id", "circuit", "width", "shots", "arrival_s",
                   "start_s", "wait_s", "target", "snapshot_id", "esp_predicted",
                   "fidelity", "floor_2q", "floor_ro", "slo_violated", "deadline_s",
                   "deadline_met", "unplaced"]
    rows = []
    for r in jobs:
        row = {}
        for c in result_cols:
            v = r.get(c)
            if isinstance(v, float):
                v = f"{v:.6f}"
            elif v is None:
                v = ""
            row[c] = v
        rows.append(row)
    write_csv(out_dir / "results.csv", rows)

    summary, per_seed, effects = summarize(jobs)
    write_csv(out_dir / "summary.csv", summary)
    write_csv(out_dir / "per_seed.csv", per_seed)
    write_csv(out_dir / "effects.csv", effects)
    if not args.no_charts:
        charts(out_dir, summary)
        esp_scatter(out_dir, jobs)
    render_report(out_dir, summary, per_seed, effects, jobs)
    print(f"wrote results/summary/per_seed/effects CSVs + report.md in {out_dir}")
    for s in summary:
        print(f"  {s['policy']:<18} fidelity={s['mean_fidelity']} "
              f"slo_viol={s['slo_violation_rate'] or 'n/a'} wait={s['mean_wait_s']}s "
              f"unplaced={s['unplaced']}")


if __name__ == "__main__":
    main()
