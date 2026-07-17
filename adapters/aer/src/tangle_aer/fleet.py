# SPDX-License-Identifier: Apache-2.0
"""TargetRuntime: binds a target config to the replay clock, serving the
current calibration snapshot and a noise model built from exactly that
snapshot (single-source principle, T4.single-source)."""

from __future__ import annotations

from collections import OrderedDict
from datetime import UTC, datetime

from qiskit_aer import AerSimulator

from .noise import build_noise_model
from .replay import DriftConfig, ReplayClock, snapshot_at
from .targets import Snapshot, TargetConfig

NOISE_CACHE_SIZE = 8


def parse_rfc3339(text: str) -> datetime:
    return datetime.fromisoformat(text.replace("Z", "+00:00")).astimezone(UTC)


class TargetRuntime:
    """One target's live view: static snapshot, or replayed drift over it."""

    def __init__(self, cfg: TargetConfig, clock: ReplayClock | None = None):
        self.cfg = cfg
        self.epoch = parse_rfc3339(cfg.snapshot.measured_at)
        self.drift = DriftConfig(**cfg.drift) if cfg.drift else None
        self.clock = clock or ReplayClock(epoch=self.epoch, accel=1.0)
        self._simulators: OrderedDict[str, AerSimulator] = OrderedDict()

    def current_snapshot(self) -> tuple[Snapshot, float]:
        """The snapshot in effect now, plus its measured-at as wall-clock
        seconds (control-plane timestamps stay in the wall timeline, D-018)."""
        if self.drift is None:
            return self.cfg.snapshot, self.epoch.timestamp()
        state = snapshot_at(self.cfg.snapshot, self.drift, self.clock.sim_now(), self.epoch)
        return state.snapshot, self.clock.wall_at(state.measured_at_sim)

    def snapshot_at_sim(self, sim_time: datetime) -> Snapshot:
        """Test/analysis hook: the snapshot at an explicit sim instant."""
        if self.drift is None:
            return self.cfg.snapshot
        return snapshot_at(self.cfg.snapshot, self.drift, sim_time, self.epoch).snapshot

    def simulator_for(self, snapshot: Snapshot) -> AerSimulator:
        """Noise model from this exact snapshot, LRU-cached by snapshot id."""
        sid = snapshot.snapshot_id
        if sid in self._simulators:
            self._simulators.move_to_end(sid)
            return self._simulators[sid]
        model = build_noise_model(self.cfg, snapshot)
        simulator = AerSimulator(noise_model=model) if model is not None else AerSimulator()
        self._simulators[sid] = simulator
        while len(self._simulators) > NOISE_CACHE_SIZE:
            self._simulators.popitem(last=False)
        return simulator
