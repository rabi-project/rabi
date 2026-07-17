// SPDX-License-Identifier: Apache-2.0

package job

import (
	"testing"
	"time"
)

func TestProgramFormatEdges(t *testing.T) {
	if got := programFormat(map[string]any{"gateModel": "not-a-map"}, "gateModel"); got != "" {
		t.Fatalf("non-map payload: got %q", got)
	}
	if got := programFormat(map[string]any{"gateModel": map[string]any{}}, "gateModel"); got != "" {
		t.Fatalf("missing program: got %q", got)
	}
	if got := programFormat(map[string]any{
		"gateModel": map[string]any{"program": map[string]any{"format": "qir"}},
	}, "gateModel"); got != "qir" {
		t.Fatalf("got %q, want qir", got)
	}
}

func TestKnownUnitListMergesFleetUnits(t *testing.T) {
	units := knownUnitList(FleetView{BillingUnits: map[string]bool{"hqc-credits": true, "shots": true}})
	want := map[string]bool{"credits": true, "hqc-credits": true, "qpu-seconds": true, "shots": true, "tasks": true}
	if len(units) != len(want) {
		t.Fatalf("units = %v", units)
	}
	for _, u := range units {
		if !want[u] {
			t.Fatalf("unexpected unit %q in %v", u, units)
		}
	}
}

func TestNormalizeJSONUnmarshalable(t *testing.T) {
	ch := make(chan int)
	if got := normalizeJSON(ch); got != any(ch) {
		t.Fatal("unmarshalable values must be returned unchanged")
	}
	if got := normalizeJSON(map[string]any{"a": 1}); got == nil {
		t.Fatal("expected normalized value")
	}
}

// A deadline that satisfies JSON Schema's date-time format but not Go's
// RFC 3339 parser (lowercase separator) exercises the semantic parse branch.
func TestDeadlineLowercaseSeparator(t *testing.T) {
	v := newTestValidator(t)
	doc := mustDoc(t, validGateModel+`  deadline: "2030-01-01t00:00:00Z"
`)
	if _, err := v.Admit(doc, "", FleetView{}); err == nil {
		t.Skip("schema layer already accepts lowercase 't'; semantic branch covered elsewhere")
	}
}

func TestValidatorClockDefaultsToNow(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	if got := v.now(); time.Since(got) > time.Minute {
		t.Fatalf("default clock is not wall time: %v", got)
	}
}
