// SPDX-License-Identifier: Apache-2.0

package store

import (
	"strings"
	"testing"
)

func TestRedactDSN(t *testing.T) {
	cases := []struct {
		name, in, mustNotContain string
		mustContain              []string
	}{
		{
			name:           "url form",
			in:             "postgres://rabi:sup3rs3cret@db.internal:5432/rabi?sslmode=disable",
			mustNotContain: "sup3rs3cret",
			mustContain:    []string{"rabi", "db.internal", "5432", "sslmode=disable", "xxxxx"},
		},
		{
			name:           "keyword form",
			in:             "host=db.internal user=rabi password=sup3rs3cret dbname=rabi",
			mustNotContain: "sup3rs3cret",
			mustContain:    []string{"host=db.internal", "user=rabi", "xxxxx"},
		},
		{
			name:        "no password",
			in:          "postgres://rabi@localhost/rabi",
			mustContain: []string{"rabi", "localhost"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactDSN(c.in)
			if c.mustNotContain != "" && strings.Contains(got, c.mustNotContain) {
				t.Errorf("redacted DSN still leaks the password: %q", got)
			}
			for _, want := range c.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("redacted DSN %q dropped %q", got, want)
				}
			}
		})
	}
	if RedactDSN("") != "" {
		t.Error("empty DSN should stay empty")
	}
}
