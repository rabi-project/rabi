// SPDX-License-Identifier: Apache-2.0

package scheduler

import "testing"

// FuzzProfileQASM drives the OpenQASM ingestion parser (P2.M4). Untrusted
// program text reaches ProfileQASM from every submitted gate-model job; it must
// never panic or hang, only profile or return an error.
func FuzzProfileQASM(f *testing.F) {
	seeds := []string{
		"",
		"OPENQASM 3.0;\nqubit[2] q;\nh q[0];\ncx q[0], q[1];\nmeasure q -> c;\n",
		"OPENQASM 2.0;\nqreg q[3];\ncreg c[3];\ncx q[0],q[1];\nrz(0.5) q[2];\n",
		"OPENQASM 3.0;\nqubit[100000] q;\n",
		"gate mygate q { h q; }\n",
		"rz(   ) q;", "cx q[999999999999999999999],q[0];",
		"garbage not qasm at all", "\x00\xff\x00", ";;;;;;",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_, _ = ProfileQASM(src) // must not panic or hang
	})
}
