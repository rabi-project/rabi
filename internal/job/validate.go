// SPDX-License-Identifier: Apache-2.0

package job

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"tangle.dev/tangle/internal/specdata"
)

// specKnownUnits are the native units named by spec/spec/overview.md §6.
// Adapters extend this set with their declared billing_units (M2+).
var specKnownUnits = map[string]bool{
	"qpu-seconds": true,
	"shots":       true,
	"tasks":       true,
	"credits":     true,
}

// payloadFieldForKind maps workload.kind to its payload field name.
var payloadFieldForKind = map[string]string{
	"gate-model":         "gateModel",
	"analog-hamiltonian": "analogHamiltonian",
	"annealing":          "annealing",
	"pulse":              "pulse",
	"logical":            "logical",
}

// Condition is a status.conditions entry (spec/spec/quantumjob.md).
type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// FleetView is what admission needs to know about the current fleet.
// Zero value = empty fleet.
type FleetView struct {
	ProgramFormats map[string]bool // formats declared by ≥1 registered target
	BillingUnits   map[string]bool // native units declared by ≥1 registered target
}

// Admission is the result of successful admission validation.
type Admission struct {
	Tenant string
	Name   string
	Doc    map[string]any // the QuantumJob document as accepted (status stripped)
	// Warnings become status.conditions; per spec, a program format the fleet
	// currently lacks is a warning, not a rejection (the fleet may change).
	Warnings []Condition
}

// Validator performs admission-time validation (spec/spec/quantumjob.md
// §Validation): JSON Schema first, then the normative semantic checks.
type Validator struct {
	schema *jsonschema.Schema
	now    func() time.Time
}

// NewValidator compiles the embedded spec schema with format and content
// assertions on (calibrationMaxAge durations, currency patterns, base64
// inline programs, RFC 3339 deadlines are all enforced, not annotated).
func NewValidator() (*Validator, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(specdata.QuantumJobSchemaJSON))
	if err != nil {
		return nil, fmt.Errorf("job: parse embedded schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.AssertContent()
	const url = "https://tangle.dev/schemas/v1alpha1/quantumjob.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("job: add schema resource: %w", err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("job: compile schema: %w", err)
	}
	return &Validator{schema: schema, now: time.Now}, nil
}

// Admit validates a QuantumJob document. requestTenant is the tenant field
// from the SubmitJobRequest envelope; empty means "take it from the document".
func (v *Validator) Admit(doc map[string]any, requestTenant string, fleet FleetView) (*Admission, error) {
	if doc == nil {
		return nil, fmt.Errorf("quantum_job document is required")
	}

	// Clients may not write status (spec: "written by the control plane only").
	if _, hasStatus := doc["status"]; hasStatus {
		return nil, fmt.Errorf("/status: may not be set by clients (written by the control plane only)")
	}

	// 1. Structural validation against the spec schema.
	if err := v.schema.Validate(normalizeJSON(doc)); err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}

	metadata := doc["metadata"].(map[string]any)
	spec := doc["spec"].(map[string]any)
	name := metadata["name"].(string)
	tenant := metadata["tenant"].(string)

	// 2. Tenant envelope consistency.
	if requestTenant != "" && requestTenant != tenant {
		return nil, fmt.Errorf("/metadata/tenant: %q does not match request tenant %q", tenant, requestTenant)
	}

	// 3. Exactly one modality payload, matching kind (the schema requires the
	// matching payload but does not forbid extras).
	workload := spec["workload"].(map[string]any)
	kind := workload["kind"].(string)
	want := payloadFieldForKind[kind]
	for _, field := range payloadFieldForKind {
		if field == want {
			continue
		}
		if _, present := workload[field]; present {
			return nil, fmt.Errorf("/spec/workload/%s: payload does not match kind %q (exactly one modality payload allowed)", field, kind)
		}
	}

	// 4. Deadline, if set, is in the future.
	if raw, ok := spec["deadline"].(string); ok {
		deadline, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("/spec/deadline: not a valid RFC 3339 timestamp: %v", err)
		}
		if !deadline.After(v.now()) {
			return nil, fmt.Errorf("/spec/deadline: %s is in the past", raw)
		}
	}

	// 5. budget.limits keys are known native units.
	if budget, ok := spec["budget"].(map[string]any); ok {
		if limits, ok := budget["limits"].(map[string]any); ok {
			for unit := range limits {
				if !specKnownUnits[unit] && !fleet.BillingUnits[unit] {
					return nil, fmt.Errorf("/spec/budget/limits/%s: unknown native unit (known: %s)", unit, strings.Join(knownUnitList(fleet), ", "))
				}
			}
		}
	}

	// 6. program.format offered by the fleet — warning, not rejection.
	var warnings []Condition
	if format := programFormat(workload, want); format != "" && !fleet.ProgramFormats[format] {
		warnings = append(warnings, Condition{
			Type:   "FormatAvailable",
			Status: "False",
			Reason: "NoRegisteredTarget",
			Message: fmt.Sprintf("no registered target currently accepts program format %q; the job will stay PENDING until one does",
				format),
		})
	}

	accepted := make(map[string]any, len(doc))
	for k, val := range doc {
		accepted[k] = val
	}
	return &Admission{Tenant: tenant, Name: name, Doc: accepted, Warnings: warnings}, nil
}

func programFormat(workload map[string]any, payloadField string) string {
	payload, ok := workload[payloadField].(map[string]any)
	if !ok {
		return ""
	}
	program, ok := payload["program"].(map[string]any)
	if !ok {
		return ""
	}
	format, _ := program["format"].(string)
	return format
}

func knownUnitList(fleet FleetView) []string {
	var units []string
	for u := range specKnownUnits {
		units = append(units, u)
	}
	for u := range fleet.BillingUnits {
		if !specKnownUnits[u] {
			units = append(units, u)
		}
	}
	sort.Strings(units)
	return units
}

// normalizeJSON round-trips a value through encoding/json semantics so the
// schema library sees canonical types (e.g. json.Number handling, structpb
// output, or test fixtures decoded from YAML all behave identically).
func normalizeJSON(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}
