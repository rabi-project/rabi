// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'wstate', 3 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[3] meas;
qubit[3] q;
sx q[0];
rz(pi/4) q[0];
sx q[0];
sx q[1];
rz(0.6154797086703874) q[1];
sx q[1];
x q[2];
cx q[2], q[1];
sx q[1];
rz(0.6154797086703869) q[1];
sx q[1];
cx q[1], q[0];
sx q[0];
rz(pi/4) q[0];
sx q[0];
cx q[1], q[2];
cx q[0], q[1];
barrier q[0], q[1], q[2];
meas[0] = measure q[0];
meas[1] = measure q[1];
meas[2] = measure q[2];

