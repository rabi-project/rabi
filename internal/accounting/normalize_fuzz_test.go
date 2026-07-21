// SPDX-License-Identifier: Apache-2.0

package accounting

import "testing"

// FuzzParsePolicy drives site normalization-policy parsing (P2.M4). A policy is
// operator-supplied YAML; ParsePolicy must never panic, only parse or error.
func FuzzParsePolicy(f *testing.F) {
	seeds := []string{
		"",
		"units:\n  shots:\n    to: credits\n    rate: 0.001\n",
		"units: {}\n",
		"units:\n  shots: not-a-map\n",
		"not: [valid, yaml",
		"units:\n  shots:\n    rate: not-a-number\n",
		"\x00\xff",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParsePolicy(data) // must not panic
	})
}
