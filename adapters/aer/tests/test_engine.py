# SPDX-License-Identifier: Apache-2.0
"""Adapter-local suite: physics sanity (T2.physics-0/1), idempotency under
concurrency (T2.idem, reduced N — the conformance harness runs the full bar),
error taxonomy, and cancellation semantics."""

from __future__ import annotations

import threading
from pathlib import Path

import pytest
from qiskit import transpile

from tangle_aer import tasks as taskmod
from tangle_aer.targets import load_config
from tangle_aer.tasks import TaskEngine

CONFIG = Path(__file__).parent.parent / "config" / "single.yaml"

BELL = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
"""

GHZ5 = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[5] q;
bit[5] c;
h q[0];
cx q[0], q[1];
cx q[1], q[2];
cx q[2], q[3];
cx q[3], q[4];
c = measure q;
"""


@pytest.fixture(scope="module")
def noisy_cfg():
    return load_config(CONFIG)[0]


@pytest.fixture()
def engine(noisy_cfg):
    return TaskEngine({noisy_cfg.target_id: noisy_cfg})


@pytest.fixture()
def ideal_cfg(noisy_cfg):
    import dataclasses

    return dataclasses.replace(noisy_cfg, noise=False)


def run_to_terminal(engine, target_id, program=BELL, shots=1000, key="k", fmt="openqasm3",
                    params=None):
    task = engine.submit(target_id, key, fmt, program, shots, params or {})
    for _snapshot in engine.watch(task.task_id):
        pass
    return engine.get(task.task_id)


def tvd(counts: dict[str, int], ideal: dict[str, float], shots: int) -> float:
    keys = set(counts) | set(ideal)
    return 0.5 * sum(abs(counts.get(k, 0) / shots - ideal.get(k, 0.0)) for k in keys)


# T2.physics-0 — noise disabled: TVD from ideal ≤ 0.01 for Bell and GHZ(5).
def test_physics0_noise_off(ideal_cfg):
    engine = TaskEngine({ideal_cfg.target_id: ideal_cfg})
    shots = 20_000

    done = run_to_terminal(engine, ideal_cfg.target_id, BELL, shots, key="bell")
    assert done.state == taskmod.SUCCEEDED, done.error
    assert tvd(done.result["counts"], {"00": 0.5, "11": 0.5}, shots) <= 0.01

    done = run_to_terminal(engine, ideal_cfg.target_id, GHZ5, shots, key="ghz")
    assert done.state == taskmod.SUCCEEDED, done.error
    assert tvd(done.result["counts"], {"00000": 0.5, "11111": 0.5}, shots) <= 0.01


# T2.physics-1 — noise on: measured Bell success within ±0.05 of the
# noise-model-predicted value (ESP-style product over the transpiled circuit).
def test_physics1_noise_on(noisy_cfg, engine):
    shots = 20_000
    done = run_to_terminal(engine, noisy_cfg.target_id, BELL, shots, key="bell-noisy")
    assert done.state == taskmod.SUCCEEDED, done.error
    measured = (done.result["counts"].get("00", 0) +
                done.result["counts"].get("11", 0)) / shots

    # Predict from the same snapshot the noise model consumed.
    from qiskit import qasm3

    circuit = qasm3.loads(BELL.decode())
    transpiled = transpile(
        circuit,
        basis_gates=list(noisy_cfg.native_gates),
        coupling_map=[list(e) for e in noisy_cfg.coupling_map],
        optimization_level=1,
        seed_transpiler=noisy_cfg.seed,
    )
    snap = noisy_cfg.snapshot
    e1 = snap.values("gate.1q.error")
    e2 = snap.values("gate.2q.cx.error")
    ero = snap.values("readout.error")
    predicted = 1.0
    layout = transpiled.layout
    measured_qubits = set()
    for inst in transpiled.data:
        qubits = [transpiled.find_bit(q).index for q in inst.qubits]
        if inst.operation.name in ("sx", "x"):
            predicted *= 1 - e1.get((qubits[0],), 0.0)
        elif inst.operation.name == "cx":
            edge = tuple(qubits)
            predicted *= 1 - e2.get(edge, e2.get(edge[::-1], 0.0))
        elif inst.operation.name == "measure":
            measured_qubits.add(qubits[0])
    for q in measured_qubits:
        predicted *= 1 - ero.get((q,), 0.0)
    _ = layout

    assert abs(measured - predicted) <= 0.05, (measured, predicted)


# T2.idem (reduced) — concurrent same-key submissions: one task, one usage set.
def test_idempotency_under_concurrency(noisy_cfg, engine):
    results = []
    barrier = threading.Barrier(50)

    def submit():
        barrier.wait()
        t = engine.submit(noisy_cfg.target_id, "same-key", "openqasm3", BELL, 100, {})
        results.append(t.task_id)

    threads = [threading.Thread(target=submit) for _ in range(50)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert len(set(results)) == 1, f"{len(set(results))} distinct tasks created"
    task_id = results[0]
    for _ in engine.watch(task_id):
        pass
    done = engine.get(task_id)
    assert done.state == taskmod.SUCCEEDED
    assert sorted(u["unit"] for u in done.usage) == ["shots", "tasks"]
    assert next(u["amount"] for u in done.usage if u["unit"] == "shots") == 100.0


def test_error_taxonomy(noisy_cfg, engine):
    tid = noisy_cfg.target_id

    bad_format = run_to_terminal(engine, tid, BELL, 100, key="fmt", fmt="qir")
    assert bad_format.state == taskmod.FAILED
    assert bad_format.error.category == taskmod.CAPABILITY_MISMATCH

    too_many_shots = run_to_terminal(engine, tid, BELL, 10**9, key="shots")
    assert too_many_shots.state == taskmod.FAILED
    assert too_many_shots.error.category == taskmod.CAPABILITY_MISMATCH

    garbage = run_to_terminal(engine, tid, b"OPENQASM 3.0; this is not qasm;", 100, key="garbage")
    assert garbage.state == taskmod.FAILED
    assert garbage.error.category == taskmod.INVALID_PROGRAM
    assert garbage.error.retriable is False

    too_wide = run_to_terminal(engine, tid, GHZ5.replace(b"[5]", b"[9]"), 100, key="wide")
    assert too_wide.state == taskmod.FAILED
    assert too_wide.error.category == taskmod.CAPABILITY_MISMATCH

    # Failed tasks consumed nothing.
    for t in (bad_format, too_many_shots, garbage, too_wide):
        assert t.usage == []


def test_cancel_queued_never_executes(noisy_cfg, engine):
    tid = noisy_cfg.target_id
    # Occupy the single worker, then cancel a queued task behind it.
    slow = engine.submit(tid, "slow", "openqasm3", BELL, 100,
                         {taskmod.DELAY_PARAM: "1500"})
    queued = engine.submit(tid, "queued", "openqasm3", BELL, 100, {})
    assert engine.cancel(queued.task_id) is True

    for _ in engine.watch(queued.task_id):
        pass
    done = engine.get(queued.task_id)
    assert done.state == taskmod.CANCELLED
    assert done.usage == []  # never ran → zero usage

    for _ in engine.watch(slow.task_id):
        pass
    assert engine.get(slow.task_id).state in (taskmod.SUCCEEDED, taskmod.CANCELLED)


def test_watch_states_move_forward(noisy_cfg, engine):
    task = engine.submit(noisy_cfg.target_id, "fwd", "openqasm3", BELL, 500,
                         {taskmod.DELAY_PARAM: "200"})
    order = {taskmod.QUEUED: 0, taskmod.RUNNING: 1, taskmod.SUCCEEDED: 2,
             taskmod.FAILED: 2, taskmod.CANCELLED: 2}
    seen = []
    last_ts = 0.0
    for snapshot in engine.watch(task.task_id):
        seen.append(snapshot.state)
        assert snapshot.updated_at >= last_ts, "timestamps must be monotonic"
        last_ts = snapshot.updated_at
    assert [order[s] for s in seen] == sorted(order[s] for s in seen)
    assert seen[-1] == taskmod.SUCCEEDED


def test_determinism_same_key_same_counts(noisy_cfg):
    e1 = TaskEngine({noisy_cfg.target_id: noisy_cfg})
    e2 = TaskEngine({noisy_cfg.target_id: noisy_cfg})
    d1 = run_to_terminal(e1, noisy_cfg.target_id, BELL, 1000, key="det")
    d2 = run_to_terminal(e2, noisy_cfg.target_id, BELL, 1000, key="det")
    assert d1.result["counts"] == d2.result["counts"]
