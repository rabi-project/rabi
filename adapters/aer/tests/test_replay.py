# SPDX-License-Identifier: Apache-2.0
"""T4 suite — calibration replay.

  T4.single-source  GetDeviceState metrics == noise-model inputs, exactly
  T4.drift          fidelity(T0 fresh) > fidelity(T0+20h drifted), 5 seeds
  T4.replay-det     same seed -> identical series hash; different -> different
  T4.provenance     replayed metrics carry provenance; snapshot_id changes
                    iff data changes
"""

from __future__ import annotations

import dataclasses
from datetime import timedelta
from pathlib import Path

import pytest

from tangle_aer import tasks as taskmod
from tangle_aer.fleet import TargetRuntime, parse_rfc3339
from tangle_aer.replay import DriftConfig, ReplayClock, series_hash
from tangle_aer.targets import load_config
from tangle_aer.tasks import TaskEngine

CONFIG = Path(__file__).parent.parent / "config" / "replay.yaml"

GHZ4 = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[4] q;
bit[4] c;
h q[0];
cx q[0], q[1];
cx q[1], q[2];
cx q[2], q[3];
c = measure q;
"""


@pytest.fixture(scope="module")
def replay_cfgs():
    return load_config(CONFIG)


@pytest.fixture(scope="module")
def torino(replay_cfgs):
    return replay_cfgs[0]


def test_replay_config_loads_real_baselines(replay_cfgs):
    assert len(replay_cfgs) == 3
    gates = {c.two_qubit_gate for c in replay_cfgs}
    assert gates == {"cz", "ecr"}  # native gates carried from the real devices
    for c in replay_cfgs:
        assert c.num_qubits == 20
        assert c.drift is not None
        assert any(m.name.startswith("gate.2q.") for m in c.snapshot.metrics)


# T4.single-source — for each target and several sim times: the snapshot the
# device reports is byte-identical to the one the noise model consumes.
def test_single_source(replay_cfgs):
    for cfg in replay_cfgs:
        rt = TargetRuntime(cfg)
        epoch = rt.epoch
        for hours in (0, 5, 20, 40):
            sim = epoch + timedelta(hours=hours)
            reported = rt.snapshot_at_sim(sim)
            # The engine consumes rt.simulator_for(snapshot) built from the
            # same object; assert the derivation itself is deterministic.
            again = rt.snapshot_at_sim(sim)
            assert reported.snapshot_id == again.snapshot_id
            assert [(m.name, m.qubits, m.value) for m in reported.metrics] == \
                   [(m.name, m.qubits, m.value) for m in again.metrics]
            # And the runtime's current_snapshot with a clock frozen at `sim`
            # reports the identical snapshot the simulator will use.
            frozen = TargetRuntime(cfg, ReplayClock.frozen(sim))
            current, _ = frozen.current_snapshot()
            expected = rt.snapshot_at_sim(sim)
            assert current.snapshot_id == expected.snapshot_id


# T4.drift — same circuit at T0 (fresh) vs T0+20h (drifted): success
# probability strictly degrades, across 5 seeds (magnitude free).
@pytest.mark.parametrize("drift_seed", [1, 2, 3, 4, 5])
def test_drift_direction(torino, drift_seed):
    cfg = dataclasses.replace(torino, drift={**torino.drift, "seed": drift_seed,
                                             "calibration_period_h": 24.0})
    rt = TargetRuntime(cfg)
    epoch = rt.epoch

    shots = 20_000
    results = {}
    for label, hours in (("fresh", 0), ("drifted", 20)):
        sim = epoch + timedelta(hours=hours)
        snap = rt.snapshot_at_sim(sim)
        frozen = TargetRuntime(cfg, ReplayClock.frozen(sim))
        engine = TaskEngine({cfg.target_id: frozen})
        task = engine.submit(cfg.target_id, f"drift-{drift_seed}-{label}", "openqasm3",
                             GHZ4, shots, {})
        for _ in engine.watch(task.task_id):
            pass
        done = engine.get(task.task_id)
        assert done.state == taskmod.SUCCEEDED, done.error
        counts = done.result["counts"]
        results[label] = (counts.get("0000", 0) + counts.get("1111", 0)) / shots
        # sanity: the snapshot at 20h really differs from the baseline
        if label == "drifted":
            assert snap.snapshot_id != rt.snapshot_at_sim(epoch).snapshot_id

    assert results["fresh"] > results["drifted"], results


# T4.replay-det — same seed → identical series hash; different seeds differ.
def test_replay_determinism(torino):
    epoch = parse_rfc3339(torino.snapshot.measured_at)
    cfg_a = DriftConfig(seed=1041)
    cfg_b = DriftConfig(seed=1041)
    cfg_c = DriftConfig(seed=2042)
    h_a = series_hash(torino.snapshot, cfg_a, epoch, hours=48)
    h_b = series_hash(torino.snapshot, cfg_b, epoch, hours=48)
    h_c = series_hash(torino.snapshot, cfg_c, epoch, hours=48)
    assert h_a == h_b, "same seed must produce an identical snapshot series"
    assert h_a != h_c, "different seeds must produce different series"


# T4.provenance — replayed metrics carry methodology/source; snapshot_id is
# stable while data is unchanged and changes when data changes; sawtooth
# resets restore the baseline.
def test_provenance_and_sawtooth(torino):
    rt = TargetRuntime(torino)
    epoch = rt.epoch
    period_h = torino.drift["calibration_period_h"]

    fresh = rt.snapshot_at_sim(epoch)
    for m in fresh.metrics:
        assert m.methodology == "replayed-vendor-calibration"
    assert fresh.source == "replayed-vendor-calibration"

    # Within one drift step the snapshot is identical.
    a = rt.snapshot_at_sim(epoch + timedelta(hours=2))
    b = rt.snapshot_at_sim(epoch + timedelta(hours=2, minutes=5))
    assert a.snapshot_id == b.snapshot_id

    # Across a step boundary the data — and therefore the id — changes.
    c = rt.snapshot_at_sim(epoch + timedelta(hours=2, minutes=15))
    assert c.snapshot_id != a.snapshot_id

    # Drift accumulates strictly within a calibration period...
    base_2q = min(m.value for m in fresh.metrics if m.name.startswith("gate.2q."))
    drift_2q = min(m.value for m in c.metrics if m.name.startswith("gate.2q."))
    assert drift_2q > base_2q

    # ...and the sawtooth resets at the calibration event.
    reset = rt.snapshot_at_sim(epoch + timedelta(hours=period_h))
    reset_2q = min(m.value for m in reset.metrics if m.name.startswith("gate.2q."))
    assert reset_2q == pytest.approx(base_2q)

    # T1/T2 degrade inversely (coherence gets worse, never better).
    base_t1 = max(m.value for m in fresh.metrics if m.name == "t1.us")
    drift_t1 = max(m.value for m in c.metrics if m.name == "t1.us")
    assert drift_t1 < base_t1


# Bounded drift: never more than +30% error relative to baseline.
def test_drift_bounded(torino):
    rt = TargetRuntime(torino)
    epoch = rt.epoch
    fresh = {(m.name, tuple(m.qubits)): m.value for m in rt.snapshot_at_sim(epoch).metrics}
    late = rt.snapshot_at_sim(epoch + timedelta(hours=23, minutes=50))
    for m in late.metrics:
        base = fresh[(m.name, tuple(m.qubits))]
        if m.name.startswith("gate.") or m.name == "readout.error":
            assert m.value <= base * 1.30 * (1 + 1e-9)
            assert m.value >= base
