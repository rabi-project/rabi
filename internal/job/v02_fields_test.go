// SPDX-License-Identifier: Apache-2.0

package job

import "testing"

// Spec v0.2 (RFC-0002/0003): the new optional fields pass admission; the
// scheduler implements their semantics in phase1 M5.
func TestAdmitAcceptsSpecV02Fields(t *testing.T) {
	v := newTestValidator(t)
	doc := mustDoc(t, validGateModel+`  requirements:
    qubits: 2
    quality:
      gateModel:
        twoQubitErrorMax: 0.006
        aggregate: median
  scheduling:
    onConflict: prefer-deadline
`)
	if _, err := v.Admit(doc, "", FleetView{}); err != nil {
		t.Fatalf("v0.2 fields must pass admission: %v", err)
	}

	bad := mustDoc(t, validGateModel+`  scheduling:
    onConflict: shrug
`)
	if _, err := v.Admit(bad, "", FleetView{}); err == nil {
		t.Fatal("unknown onConflict value must be rejected")
	}
}
