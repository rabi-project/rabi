// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'ghz', 4 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[4] meas;
qubit[4] q;
rz(pi/2) q[3];
sx q[3];
rz(pi/2) q[3];
cx q[3], q[2];
cx q[2], q[1];
cx q[1], q[0];
barrier q[0], q[1], q[2], q[3];
meas[0] = measure q[0];
meas[1] = measure q[1];
meas[2] = measure q[2];
meas[3] = measure q[3];

