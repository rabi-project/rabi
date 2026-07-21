// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"encoding/json"
	"testing"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
)

// FuzzPayloadFor drives payload extraction (P2.M4): it reads program.format and
// base64-decodes program.inline out of an untrusted job document. It must never
// panic, only return a payload or an error detail.
func FuzzPayloadFor(f *testing.F) {
	seeds := []string{
		`{"spec":{"workload":{"kind":"gate-model","gateModel":{"program":{"format":"openqasm3","inline":"T1BFTlFBU00gMy4wOw=="},"shots":1000}}}}`,
		`{"spec":{"workload":{"kind":"gate-model","gateModel":{"program":{"format":"openqasm3","inline":"!!!not base64!!!"}}}}}`,
		`{"spec":{"workload":{"kind":"gate-model","gateModel":{"program":{"format":123,"source":"http://x"}}}}}`,
		`{"spec":{"workload":{"kind":"annealing"}}}`,
		`{}`,
		`{"spec":{"workload":{"kind":"gate-model","gateModel":{"shots":"lots"}}}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return
		}
		_, _, _ = payloadFor(doc) // must not panic
	})
}

// FuzzResultDecode drives adapter-result decoding (P2.M4): resultToMap parses
// the adapter's inline result body (the "counts-json" payload) with
// json.Unmarshal, falling back to base64. A hostile or corrupt adapter body
// must never crash the control plane — it reaches a queryable state instead.
func FuzzResultDecode(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"counts":{"00":500,"11":500}}`),
		[]byte(`{"counts":{}}`),
		[]byte("\x00\xff not json at all \xfe"),
		[]byte(`[1,2,3]`),
		[]byte(`"just a string"`),
		[]byte(`{"counts":{"00":9e999}}`),
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		r := &adapterv1alpha1.Result{
			Format: "counts-json",
			Body:   &adapterv1alpha1.Result_Inline{Inline: body},
		}
		_ = resultToMap(r) // must not panic
	})
}
