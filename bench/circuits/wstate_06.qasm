// SPDX-License-Identifier: MIT
// Source: MQT Bench v2.2.3 — benchmark 'wstate', 6 qubits,
// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx
// (optimization_level=1, seed_transpiler=7) by
// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,
// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.
OPENQASM 3.0;
include "stdgates.inc";
bit[6] meas;
qubit[6] q;
sx q[0];
rz(pi/4) q[0];
sx q[0];
sx q[1];
rz(0.6154797086703874) q[1];
sx q[1];
sx q[2];
rz(pi/6) q[2];
sx q[2];
sx q[3];
rz(0.46364760900080615) q[3];
sx q[3];
sx q[4];
rz(0.42053433528396456) q[4];
sx q[4];
x q[5];
cx q[5], q[4];
sx q[4];
rz(0.42053433528396456) q[4];
sx q[4];
cx q[4], q[3];
sx q[3];
rz(0.46364760900080615) q[3];
sx q[3];
cx q[3], q[2];
sx q[2];
rz(pi/6) q[2];
sx q[2];
cx q[2], q[1];
sx q[1];
rz(0.6154797086703869) q[1];
sx q[1];
cx q[1], q[0];
sx q[0];
rz(pi/4) q[0];
sx q[0];
cx q[4], q[5];
cx q[3], q[4];
cx q[2], q[3];
cx q[1], q[2];
cx q[0], q[1];
barrier q[0], q[1], q[2], q[3], q[4], q[5];
meas[0] = measure q[0];
meas[1] = measure q[1];
meas[2] = measure q[2];
meas[3] = measure q[3];
meas[4] = measure q[4];
meas[5] = measure q[5];

