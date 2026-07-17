// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'grover', 3 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[3] meas;
qubit[2] q;
qubit[1] flag;
rz(pi/2) q[0];
sx q[0];
rz(pi/2) q[0];
rz(pi/2) q[1];
sx q[1];
rz(pi/2) q[1];
x flag[0];
cx q[0], flag[0];
rz(-pi/4) flag[0];
cx q[1], flag[0];
rz(pi/4) flag[0];
cx q[0], flag[0];
rz(-pi/4) flag[0];
cx q[1], flag[0];
rz(pi/4) q[1];
cx q[0], q[1];
rz(-pi/4) q[1];
cx q[0], q[1];
rz(-pi/4) q[0];
sx q[0];
rz(pi/2) q[0];
rz(-pi) q[1];
cx q[0], q[1];
rz(pi/2) q[0];
sx q[0];
rz(-pi/2) q[0];
rz(-pi) q[1];
rz(pi/4) flag[0];
barrier q[0], q[1], flag[0];
meas[0] = measure q[0];
meas[1] = measure q[1];
meas[2] = measure flag[0];

