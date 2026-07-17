// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'dj', 6 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[5] c;
qubit[6] q;
rz(-pi/2) q[0];
sx q[0];
rz(pi/2) q[0];
rz(-pi/2) q[1];
sx q[1];
rz(pi/2) q[1];
rz(pi/2) q[2];
sx q[2];
rz(pi/2) q[2];
rz(pi/2) q[3];
sx q[3];
rz(pi/2) q[3];
rz(-pi/2) q[4];
sx q[4];
rz(pi/2) q[4];
rz(-3*pi/2) q[5];
sx q[5];
rz(-pi/2) q[5];
cx q[0], q[5];
rz(pi/2) q[0];
sx q[0];
rz(-pi/2) q[0];
cx q[1], q[5];
rz(pi/2) q[1];
sx q[1];
rz(-pi/2) q[1];
cx q[2], q[5];
rz(pi/2) q[2];
sx q[2];
rz(pi/2) q[2];
cx q[3], q[5];
rz(pi/2) q[3];
sx q[3];
rz(pi/2) q[3];
cx q[4], q[5];
rz(pi/2) q[4];
sx q[4];
rz(-pi/2) q[4];
barrier q[0], q[1], q[2], q[3], q[4], q[5];
c[0] = measure q[0];
c[1] = measure q[1];
c[2] = measure q[2];
c[3] = measure q[3];
c[4] = measure q[4];

