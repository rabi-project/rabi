// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// CircuitProfile is the deterministic gate-count estimate that feeds ESP
// scoring. It comes from a light lexical scan of flat OpenQASM 2/3 — the
// "precomputed per-target depth estimates" option of mvp-build-plan.md §5,
// recorded in docs/decisions.md D-021. Control flow, subroutines, and gate
// definitions are out of scope for v0 (an error, not a guess).
type CircuitProfile struct {
	Qubits         int
	OneQubitGates  int
	TwoQubitGates  int
	MeasuredQubits int
}

var oneQubitGates = map[string]bool{
	"id": true, "x": true, "y": true, "z": true, "h": true, "s": true,
	"sdg": true, "t": true, "tdg": true, "sx": true, "sxdg": true,
	"rx": true, "ry": true, "rz": true, "p": true, "u": true,
	"u1": true, "u2": true, "u3": true, "reset": true,
}

var twoQubitGates = map[string]bool{
	"cx": true, "cy": true, "cz": true, "ch": true, "cp": true, "swap": true,
	"crx": true, "cry": true, "crz": true, "cu": true, "cu1": true, "cu3": true,
	"rxx": true, "ryy": true, "rzz": true, "rzx": true, "ecr": true, "iswap": true,
	"dcx": true, "csx": true,
}

// threeQubitDecomp counts standard decompositions into the 1q/2q basis.
var threeQubitDecomp = map[string]struct{ oneQ, twoQ int }{
	"ccx":   {9, 6},
	"ccz":   {9, 6},
	"cswap": {11, 8},
}

var (
	qubitDeclRe = regexp.MustCompile(`^\s*(?:qubit\[(\d+)\]|qreg\s+\w+\[(\d+)\])`)
	gateCallRe  = regexp.MustCompile(`^\s*([a-z_][a-zA-Z0-9_]*)\s*(?:\([^)]*\))?\s+(.+);`)
	// qasm2: measure q[0] -> c[0];  or  measure q -> c;
	measure2Re = regexp.MustCompile(`^\s*measure\s+(\w+)(\[(\d+)\])?\s*->`)
	// qasm3: c = measure q;  or  c[0] = measure q[0];
	measure3Re = regexp.MustCompile(`^\s*\w+(\[\d+\])?\s*=\s*measure\s+(\w+)(\[(\d+)\])?`)
)

// ProfileQASM scans a flat OpenQASM 2/3 program.
func ProfileQASM(src string) (CircuitProfile, error) {
	var p CircuitProfile
	registers := map[string]int{}

	for lineNo, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		lower := line

		switch {
		case strings.HasPrefix(lower, "OPENQASM"), strings.HasPrefix(lower, "include"),
			strings.HasPrefix(lower, "bit"), strings.HasPrefix(lower, "creg"),
			strings.HasPrefix(lower, "barrier"):
			continue
		}

		if m := qubitDeclRe.FindStringSubmatch(line); m != nil {
			size := m[1]
			if size == "" {
				size = m[2]
			}
			n, _ := strconv.Atoi(size)
			p.Qubits += n
			// register name: qasm3 "qubit[n] q;" / qasm2 "qreg q[n];"
			fields := strings.Fields(strings.TrimSuffix(line, ";"))
			name := fields[len(fields)-1]
			name = strings.Split(name, "[")[0]
			registers[name] = n
			continue
		}

		if m := measure2Re.FindStringSubmatch(line); m != nil {
			if m[2] != "" {
				p.MeasuredQubits++
			} else {
				p.MeasuredQubits += registers[m[1]]
			}
			continue
		}
		if m := measure3Re.FindStringSubmatch(line); m != nil {
			if m[3] != "" {
				p.MeasuredQubits++
			} else {
				p.MeasuredQubits += registers[m[2]]
			}
			continue
		}

		if m := gateCallRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			switch {
			case oneQubitGates[name]:
				p.OneQubitGates++
			case twoQubitGates[name]:
				p.TwoQubitGates++
			default:
				if d, ok := threeQubitDecomp[name]; ok {
					p.OneQubitGates += d.oneQ
					p.TwoQubitGates += d.twoQ
					continue
				}
				return p, fmt.Errorf("scheduler: line %d: unsupported statement %q (flat QASM only, D-021)",
					lineNo+1, name)
			}
			continue
		}

		return p, fmt.Errorf("scheduler: line %d: cannot profile statement %q", lineNo+1, line)
	}

	if p.MeasuredQubits > p.Qubits {
		p.MeasuredQubits = p.Qubits
	}
	return p, nil
}
