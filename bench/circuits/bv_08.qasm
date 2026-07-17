// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'bv', 8 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[7] c;
qubit[8] q;
rz(pi/2) q[0];
sx q[0];
rz(-pi/2) q[0];
rz(pi/2) q[2];
sx q[2];
rz(pi/2) q[2];
cx q[2], q[0];
rz(pi/2) q[2];
sx q[2];
rz(pi/2) q[2];
rz(pi/2) q[4];
sx q[4];
rz(pi/2) q[4];
cx q[4], q[0];
rz(pi/2) q[4];
sx q[4];
rz(pi/2) q[4];
rz(pi/2) q[6];
sx q[6];
rz(pi/2) q[6];
cx q[6], q[0];
rz(pi/2) q[0];
sx q[0];
rz(pi/2) q[0];
rz(pi/2) q[6];
sx q[6];
rz(pi/2) q[6];
c[0] = measure q[1];
c[1] = measure q[2];
c[2] = measure q[3];
c[3] = measure q[4];
c[4] = measure q[5];
c[5] = measure q[6];
c[6] = measure q[7];

