// SPDX-License-Identifier: Apache-2.0

// Package specdata embeds machine-readable artifacts from the vendored spec.
// Go embed cannot reach outside the package directory, so `make gen` copies
// spec/schemas/quantumjob.schema.json here and a unit test asserts the copy
// is byte-identical to the spec (docs/decisions.md D-009). Never edit the
// copy by hand.
package specdata

import _ "embed"

//go:embed quantumjob.schema.json
var QuantumJobSchemaJSON []byte
