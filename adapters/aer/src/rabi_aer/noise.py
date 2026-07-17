# SPDX-License-Identifier: Apache-2.0
"""Noise model construction from a calibration snapshot.

The model consumes exactly the metrics that GetDeviceState reports
(single-source principle, T4.single-source): depolarizing error per gate from
reported gate errors, readout error from reported readout error, and T1/T2
thermal relaxation where present.

Metric vocabulary (namespaced per spec/spec/overview.md §5):
  gate.1q.error       [q]     one-qubit gate error probability
  gate.2q.<gate>.error[a,b]   two-qubit gate error probability (cx/cz/ecr/...)
  readout.error       [q]     readout assignment error probability
  t1.us / t2.us       [q]     relaxation/dephasing times, microseconds
"""

from __future__ import annotations

from qiskit_aer.noise import (
    NoiseModel,
    ReadoutError,
    depolarizing_error,
    thermal_relaxation_error,
)

from .targets import Snapshot, TargetConfig

# Fixed gate durations for the relaxation channel (IBM-typical orders).
GATE_1Q_NS = 35.0
GATE_2Q_NS = 300.0

ONE_QUBIT_GATES = ("sx", "x")


def build_noise_model(target: TargetConfig, snapshot: Snapshot | None = None) -> NoiseModel | None:
    """Noise model from the given snapshot (default: the target's static
    snapshot); None when the target is configured noiseless."""
    if not target.noise:
        return None
    snap = snapshot if snapshot is not None else target.snapshot
    twoq = target.two_qubit_gate
    model = NoiseModel(basis_gates=list(target.native_gates))

    e1 = snap.values("gate.1q.error")
    e2 = snap.values(f"gate.2q.{twoq}.error")
    ero = snap.values("readout.error")
    t1s = snap.values("t1.us")
    t2s = snap.values("t2.us")

    for q in range(target.num_qubits):
        err1 = e1.get((q,))
        t1 = t1s.get((q,))
        t2 = t2s.get((q,))
        if t1 is not None and t2 is not None:
            # Physical constraint: T2 <= 2*T1.
            t2 = min(t2, 2 * t1)
        error = None
        if err1 is not None:
            error = depolarizing_error(err1, 1)
        if t1 is not None and t2 is not None:
            relax = thermal_relaxation_error(t1 * 1000, t2 * 1000, GATE_1Q_NS)
            error = relax if error is None else error.compose(relax)
        if error is not None:
            model.add_quantum_error(error, list(ONE_QUBIT_GATES), [q])

        ro = ero.get((q,))
        if ro is not None:
            model.add_readout_error(ReadoutError([[1 - ro, ro], [ro, 1 - ro]]), [q])

    seen_edges = set()
    for (a, b), err2 in e2.items():
        edge = (min(a, b), max(a, b))
        if edge in seen_edges:  # snapshots may list both directions
            continue
        seen_edges.add(edge)
        error = depolarizing_error(err2, 2)
        if (a,) in t1s and (a,) in t2s and (b,) in t1s and (b,) in t2s:
            relax_a = thermal_relaxation_error(
                t1s[(a,)] * 1000, min(t2s[(a,)], 2 * t1s[(a,)]) * 1000, GATE_2Q_NS
            )
            relax_b = thermal_relaxation_error(
                t1s[(b,)] * 1000, min(t2s[(b,)], 2 * t1s[(b,)]) * 1000, GATE_2Q_NS
            )
            error = error.compose(relax_a.expand(relax_b))
        model.add_quantum_error(error, [twoq], [a, b])
        model.add_quantum_error(error, [twoq], [b, a])

    return model
