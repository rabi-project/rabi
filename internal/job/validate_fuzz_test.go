// SPDX-License-Identifier: Apache-2.0

package job

import (
	"encoding/json"
	"testing"
)

// FuzzAdmit drives the admission validator (P2.M4). Every submitted QuantumJob
// document reaches Admit as untrusted, attacker-controlled JSON. Admit may
// reject with an error, but it must NEVER panic — in particular the semantic
// checks that run after JSON-Schema validation must not assume a type the
// schema did not guarantee.
func FuzzAdmit(f *testing.F) {
	v, err := NewValidator()
	if err != nil {
		f.Fatalf("validator: %v", err)
	}
	fleet := FleetView{
		ProgramFormats: map[string]bool{"openqasm3": true, "qir": true},
		BillingUnits:   map[string]bool{"shots": true, "qpu-seconds": true},
	}
	seeds := []string{
		`{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"x","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model","gateModel":{"program":{"format":"openqasm3","inline":"T1BFTlFBU00gMy4wOw=="},"shots":1000}},"requirements":{"qubits":2}}}`,
		`{}`,
		`{"metadata":42}`,
		`{"metadata":{"name":123,"tenant":[]}}`,
		`{"kind":"QuantumJob","metadata":{"name":"x","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}`,
		`{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"x","tenant":"acme/qa"},"spec":{"workload":{"kind":9},"requirements":{"qubits":"lots"}}}`,
		`{"spec":{"workload":{"kind":"gate-model","gateModel":{"program":{"format":123,"inline":true}}}}}`,
		`{"spec":{"budget":{"maxCost":{"amount":"x","currency":9}}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return // only structurally-valid JSON objects reach Admit in practice
		}
		_, _ = v.Admit(doc, "acme/qa", fleet) // must not panic
	})
}
