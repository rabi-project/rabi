# SPDX-License-Identifier: Apache-2.0
"""Calibration replay: a fleet-wide simulated clock plus synthetic drift over
real calibration baselines (mvp-build-plan.md §4).

Honesty note (this wording also appears in the benchmark report): fake
backends provide one real snapshot per device. We synthesize a time series —
a seeded, strictly-degrading random walk on error metrics, bounded at
+MAX_DRIFT relative to the baseline, with a sawtooth reset at simulated
calibration events every `calibration_period_h`. T1/T2 degrade inversely.
This is *synthetic drift over real calibration baselines*, not measured
longitudinal data.

Determinism: every value is a pure function of (seed, metric identity,
calibration cycle, step index) — no process state, so replays and concurrent
readers always agree.
"""

from __future__ import annotations

import hashlib
import json
import random
import struct
import time
from dataclasses import dataclass, field, replace
from datetime import datetime, timedelta

from .targets import Metric, Snapshot

# Error metrics drift up; coherence times (t1/t2) drift down by the same walk.
ERROR_METRICS = ("gate.1q.error", "readout.error")  # + any gate.2q.*.error
COHERENCE_METRICS = ("t1.us", "t2.us")


@dataclass(frozen=True)
class DriftConfig:
    seed: int
    calibration_period_h: float = 24.0
    step_minutes: float = 10.0
    max_drift: float = 0.30            # cap: +30% error relative to baseline
    degradation_per_hour: float = 0.02 # deterministic upward bias
    noise_per_step: float = 0.004      # |N(0, sigma)| extra per step


class ReplayClock:
    """Fleet-wide simulated time: 1 wall second = `accel` sim seconds.

    The sim world starts at `epoch` when the process starts (or an explicit
    wall_start for tests). wall_at(sim) maps sim instants back to wall time so
    control-plane-facing timestamps stay in the wall timeline (D-018).
    """

    def __init__(self, epoch: datetime, accel: float = 1.0, wall_start: float | None = None):
        self.epoch = epoch
        self.accel = accel
        self.wall_start = time.time() if wall_start is None else wall_start

    def sim_now(self) -> datetime:
        return self.sim_at(time.time())

    def sim_at(self, wall: float) -> datetime:
        return self.epoch + timedelta(seconds=(wall - self.wall_start) * self.accel)

    def wall_at(self, sim: datetime) -> float:
        if self.accel == 0:  # frozen clock (tests): everything is "now"
            return self.wall_start
        return self.wall_start + (sim - self.epoch).total_seconds() / self.accel

    @classmethod
    def frozen(cls, sim: datetime) -> ReplayClock:
        """A clock pinned at one sim instant — deterministic tests."""
        return cls(epoch=sim, accel=0.0, wall_start=time.time())


def _metric_key(m: Metric) -> str:
    return f"{m.name}@{','.join(map(str, m.qubits))}"


def _walk(cfg: DriftConfig, metric_key: str, cycle: int, steps: int) -> float:
    """Cumulative degradation after `steps` drift steps within one cycle.

    Strictly non-decreasing in steps (each increment >= bias), so drifted
    error is always worse than fresh calibration — the sawtooth's rising edge.
    """
    step_h = cfg.step_minutes / 60.0
    total = 0.0
    for k in range(steps):
        seed_material = f"{cfg.seed}:{metric_key}:{cycle}:{k}".encode()
        digest = hashlib.sha256(seed_material).digest()
        rng = random.Random(struct.unpack("<Q", digest[:8])[0])
        total += cfg.degradation_per_hour * step_h + abs(rng.gauss(0.0, cfg.noise_per_step))
    return min(total, cfg.max_drift)


@dataclass
class ReplayState:
    """Derived state for one target at one sim instant."""
    snapshot: Snapshot
    cycle: int
    step: int
    sim_time: datetime
    measured_at_sim: datetime = field(default=None)  # type: ignore[assignment]


def snapshot_at(baseline: Snapshot, cfg: DriftConfig, sim_time: datetime,
                epoch: datetime) -> ReplayState:
    """The drifted snapshot the device exhibits (and reports) at sim_time."""
    elapsed = max(0.0, (sim_time - epoch).total_seconds())
    period_s = cfg.calibration_period_h * 3600.0
    step_s = cfg.step_minutes * 60.0
    cycle = int(elapsed // period_s)
    phase = elapsed - cycle * period_s
    step = int(phase // step_s)

    metrics = []
    for m in baseline.metrics:
        walk = _walk(cfg, _metric_key(m), cycle, step)
        if m.name.startswith("gate.2q.") or m.name in ERROR_METRICS:
            value = min(m.value * (1.0 + walk), 1.0)
        elif m.name in COHERENCE_METRICS:
            value = m.value / (1.0 + walk)
        else:
            value = m.value
        metrics.append(replace(m, value=value, methodology="replayed-vendor-calibration"))

    content = json.dumps(
        [[m.name, m.qubits, round(m.value, 12)] for m in metrics], sort_keys=True)
    digest = hashlib.sha256(content.encode()).hexdigest()[:12]
    snap = Snapshot(
        snapshot_id=f"replay-{digest}",
        measured_at=baseline.measured_at,  # reformatted by the service layer
        source="replayed-vendor-calibration",
        metrics=metrics,
    )
    state = ReplayState(snapshot=snap, cycle=cycle, step=step, sim_time=sim_time)
    state.measured_at_sim = epoch + timedelta(seconds=cycle * period_s + step * step_s)
    return state


def series_hash(baseline: Snapshot, cfg: DriftConfig, epoch: datetime,
                hours: float, samples: int = 24) -> str:
    """Hash of a sampled drift series — determinism/collision checks (T4.replay-det)."""
    ids = []
    for i in range(samples):
        sim = epoch + timedelta(hours=hours * i / max(1, samples - 1))
        ids.append(snapshot_at(baseline, cfg, sim, epoch).snapshot.snapshot_id)
    return hashlib.sha256("|".join(ids).encode()).hexdigest()
