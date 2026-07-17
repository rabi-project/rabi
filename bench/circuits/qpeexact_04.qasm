// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'qpeexact', 4 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[3] c;
qubit[3] q;
qubit[1] psi;
rz(pi/2) q[0];
sx q[0];
rz(pi/2) q[0];
rz(pi/2) q[1];
sx q[1];
rz(pi/4) q[1];
cx q[1], q[2];
rz(pi/4) q[2];
cx q[1], q[2];
rz(pi/2) q[1];
sx q[1];
rz(pi/2) q[1];
rz(-pi/4) q[2];
x psi[0];
rz(pi/2) psi[0];
cx psi[0], q[0];
rz(-pi/2) q[0];
cx psi[0], q[0];
rz(3*pi/8) q[0];
cx q[0], q[2];
rz(pi/8) q[2];
cx q[0], q[2];
rz(-pi/4) q[0];
cx q[0], q[1];
rz(pi/4) q[1];
cx q[0], q[1];
rz(pi/2) q[0];
sx q[0];
rz(pi/2) q[0];
rz(-pi/4) q[1];
rz(-pi/8) q[2];
barrier q[2], q[1], q[0], psi[0];
c[0] = measure q[2];
c[1] = measure q[1];
c[2] = measure q[0];

