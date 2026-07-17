// SPDX-License-Identifier: Apache-2.0

package scheduler

import "testing"

func TestProfileQASM(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    CircuitProfile
		wantErr bool
	}{
		{"qasm3 bell", `
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
`, CircuitProfile{Qubits: 2, OneQubitGates: 1, TwoQubitGates: 1, MeasuredQubits: 2}, false},
		{"qasm2 ghz", `
OPENQASM 2.0;
include "qelib1.inc";
qreg q[3];
creg c[3];
h q[0];
cx q[0], q[1];
cx q[1], q[2];
measure q[0] -> c[0];
measure q[1] -> c[1];
measure q[2] -> c[2];
`, CircuitProfile{Qubits: 3, OneQubitGates: 1, TwoQubitGates: 2, MeasuredQubits: 3}, false},
		{"parameterized gates", `
OPENQASM 3.0;
qubit[2] q;
bit[2] c;
rz(0.5) q[0];
rzz(1.2) q[0], q[1];
c = measure q;
`, CircuitProfile{Qubits: 2, OneQubitGates: 1, TwoQubitGates: 1, MeasuredQubits: 2}, false},
		{"toffoli decomposition", `
OPENQASM 3.0;
qubit[3] q;
bit[3] c;
ccx q[0], q[1], q[2];
c = measure q;
`, CircuitProfile{Qubits: 3, OneQubitGates: 9, TwoQubitGates: 6, MeasuredQubits: 3}, false},
		{"barrier and comments ignored", `
OPENQASM 3.0;
qubit[1] q;
bit[1] c;
// a comment
x q[0]; // trailing comment
barrier q;
c[0] = measure q[0];
`, CircuitProfile{Qubits: 1, OneQubitGates: 1, MeasuredQubits: 1}, false},
		{"control flow rejected", `
OPENQASM 3.0;
qubit[1] q;
for uint i in [0:4] { x q[0]; }
`, CircuitProfile{}, true},
		{"unknown gate rejected", `
OPENQASM 3.0;
qubit[2] q;
mygate q[0], q[1];
`, CircuitProfile{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProfileQASM(tc.src)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("profile = %+v, want %+v", got, tc.want)
			}
		})
	}
}
