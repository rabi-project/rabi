// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'ghz', 2 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[2] meas;
qubit[2] q;
rz(pi/2) q[1];
sx q[1];
rz(pi/2) q[1];
cx q[1], q[0];
barrier q[0], q[1];
meas[0] = measure q[0];
meas[1] = measure q[1];

