// SPDX-License-Identifier: Apache-2.0

package specdata

import (
	"bytes"
	"os"
	"testing"
)

// The embedded schema must stay byte-identical to the vendored spec — the
// spec is the source of truth, the embed is a build artifact (D-009).
func TestEmbeddedSchemaMatchesSpec(t *testing.T) {
	specCopy, err := os.ReadFile("../../spec/schemas/quantumjob.schema.json")
	if err != nil {
		t.Fatalf("reading spec schema: %v", err)
	}
	if !bytes.Equal(specCopy, QuantumJobSchemaJSON) {
		t.Fatal("internal/specdata/quantumjob.schema.json differs from spec/schemas/quantumjob.schema.json; run 'make gen'")
	}
}
