// SPDX-License-Identifier: Apache-2.0

package job

import (
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

func mustDoc(t *testing.T, y string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return doc
}

func newTestValidator(t *testing.T) *Validator {
	t.Helper()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	// Frozen clock: fixtures use fixed deadlines (T&V plan: fake the clock).
	v.now = func() time.Time { return time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC) }
	return v
}

const validGateModel = `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata:
  name: bell-run
  tenant: acme/qa
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00gMy4wOw== }
      shots: 1000
`

// T1.schema — table-driven admission validation against the spec schema plus
// the normative semantic rules in spec/spec/quantumjob.md §Validation.
func TestAdmit(t *testing.T) {
	v := newTestValidator(t)

	valid := []struct {
		name string
		doc  string
	}{
		{"gate-model minimal", validGateModel},
		{"gate-model full-featured", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata:
  name: vqe-fe2s2-run14
  tenant: yonsei-chem-lab
  labels: { experiment: fe2s2 }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, source: "s3://bucket/ansatz.qasm" }
      shots: 20000
  requirements:
    qubits: 24
    technology: [superconducting, trapped-ion]
    quality:
      gateModel:
        twoQubitErrorMax: 0.006
        readoutErrorMax: 0.02
        calibrationMaxAge: 6h
  coupling: co-located
  session: { join: null, maxDuration: 2h }
  bundle:
    classical: { gpus: 0 }
    interconnect: none
  deadline: "2030-07-15T09:00:00+09:00"
  budget:
    maxCost: { amount: 25, currency: USD }
    limits: { qpu-seconds: 120, shots: 20000 }
  backendSelector:
    preferOnPrem: true
    allowCloudBurst: [ibm_torino]
    denyTargets: []
  retryOf: null
`},
		{"analog-hamiltonian", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: rydberg-sweep, tenant: acme }
spec:
  workload:
    kind: analog-hamiltonian
    analogHamiltonian:
      program: { format: pulser, source: "s3://bucket/seq.json" }
      repetitions: 200
`},
		{"annealing", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: qubo-portfolio, tenant: acme }
spec:
  workload:
    kind: annealing
    annealing:
      program: { format: vendor-native, source: "s3://bucket/qubo.json" }
      reads: 500
      schedule: { annealing_time_us: 20 }
`},
		{"pulse", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: rabi-cal, tenant: acme }
spec:
  workload:
    kind: pulse
    pulse:
      program: { format: vendor-native, inline: cGxzOjEuMA== }
      shots: 100
`},
		{"logical", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: logical-memory, tenant: acme }
spec:
  workload:
    kind: logical
    logical:
      program: { format: qir, source: "s3://bucket/logical.bc" }
      logicalQubits: 2
      targetLogicalErrorRate: 0.001
`},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			adm, err := v.Admit(mustDoc(t, tc.doc), "", FleetView{})
			if err != nil {
				t.Fatalf("expected admission, got: %v", err)
			}
			if adm.Tenant == "" || adm.Name == "" {
				t.Fatalf("admission missing identity: %+v", adm)
			}
		})
	}

	rejections := []struct {
		name    string
		doc     string
		wantErr string // substring the precise error must contain
	}{
		{"unknown workload.kind", strings.Replace(validGateModel, "kind: gate-model", "kind: photonic", 1),
			"/spec/workload/kind"},
		{"missing payload for kind", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload: { kind: gate-model }
`, "/spec/workload"},
		{"extra non-matching payload", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00= }
      shots: 10
    annealing:
      program: { format: vendor-native, inline: T1BFTlFBU00= }
`, "payload does not match kind"},
		{"bad calibrationMaxAge duration", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00= }
      shots: 10
  requirements:
    quality:
      gateModel: { calibrationMaxAge: "6 hours" }
`, "calibrationMaxAge"},
		{"bad currency", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00= }
      shots: 10
  budget:
    maxCost: { amount: 5, currency: usd }
`, "currency"},
		{"program with both inline and source", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00=, source: "s3://b/x.qasm" }
      shots: 10
`, "program"},
		{"program with neither inline nor source", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3 }
      shots: 10
`, "program"},
		{"zero shots", strings.Replace(validGateModel, "shots: 1000", "shots: 0", 1), "shots"},
		{"missing shots", strings.Replace(validGateModel, "      shots: 1000\n", "", 1), "shots"},
		{"uppercase name", strings.Replace(validGateModel, "name: bell-run", "name: BellRun", 1), "/metadata/name"},
		{"missing tenant", strings.Replace(validGateModel, "  tenant: acme/qa\n", "", 1), "tenant"},
		{"wrong apiVersion", strings.Replace(validGateModel, "apiVersion: tangle.dev/v1alpha1", "apiVersion: tangle.dev/v1", 1), "apiVersion"},
		{"wrong document kind", strings.Replace(validGateModel, "kind: QuantumJob", "kind: Job", 1), "/kind"},
		{"zero qubits", validGateModel + `  requirements:
    qubits: 0
`, "qubits"},
		{"zero maxCost amount", validGateModel + `  budget:
    maxCost: { amount: 0, currency: USD }
`, "amount"},
		{"unknown spec field", validGateModel + `  priority: 9
`, "priority"},
		{"deadline in the past", validGateModel + `  deadline: "2020-01-01T00:00:00Z"
`, "in the past"},
		{"deadline not a timestamp", validGateModel + `  deadline: tomorrow
`, "/spec/deadline"},
		{"unknown budget limit unit", validGateModel + `  budget:
    limits: { gpu-hours: 4 }
`, "/spec/budget/limits/gpu-hours"},
		{"twoQubitErrorMax out of range", validGateModel + `  requirements:
    quality:
      gateModel: { twoQubitErrorMax: 1.5 }
`, "twoQubitErrorMax"},
		{"non-string label", strings.Replace(validGateModel, "  tenant: acme/qa",
			"  tenant: acme/qa\n  labels: { attempt: 3 }", 1), "/metadata/labels"},
		{"inline program not base64", strings.Replace(validGateModel,
			"inline: T1BFTlFBU00gMy4wOw==", `inline: "!!not-base64!!"`, 1), "inline"},
		{"logical error rate zero", `
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: j, tenant: t }
spec:
  workload:
    kind: logical
    logical:
      program: { format: qir, source: "s3://b/l.bc" }
      targetLogicalErrorRate: 0
`, "targetLogicalErrorRate"},
		{"client-written status", validGateModel + `status:
  phase: SUCCEEDED
`, "/status"},
	}
	for _, tc := range rejections {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			_, err := v.Admit(mustDoc(t, tc.doc), "", FleetView{})
			if err == nil {
				t.Fatal("expected rejection, job was admitted")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.wantErr)
			}
		})
	}

	t.Run("reject/nil document", func(t *testing.T) {
		if _, err := v.Admit(nil, "", FleetView{}); err == nil {
			t.Fatal("expected rejection of nil document")
		}
	})

	t.Run("reject/tenant envelope mismatch", func(t *testing.T) {
		_, err := v.Admit(mustDoc(t, validGateModel), "other-tenant", FleetView{})
		if err == nil || !strings.Contains(err.Error(), "does not match request tenant") {
			t.Fatalf("expected tenant mismatch rejection, got: %v", err)
		}
	})

	t.Run("warn/format unknown to fleet", func(t *testing.T) {
		adm, err := v.Admit(mustDoc(t, validGateModel), "", FleetView{})
		if err != nil {
			t.Fatalf("format gap must warn, not reject: %v", err)
		}
		if len(adm.Warnings) != 1 || adm.Warnings[0].Type != "FormatAvailable" {
			t.Fatalf("expected one FormatAvailable warning, got %+v", adm.Warnings)
		}
	})

	t.Run("no warning when fleet has format", func(t *testing.T) {
		adm, err := v.Admit(mustDoc(t, validGateModel), "",
			FleetView{ProgramFormats: map[string]bool{"openqasm3": true}})
		if err != nil {
			t.Fatalf("unexpected rejection: %v", err)
		}
		if len(adm.Warnings) != 0 {
			t.Fatalf("expected no warnings, got %+v", adm.Warnings)
		}
	})

	t.Run("fleet billing units extend known set", func(t *testing.T) {
		doc := mustDoc(t, validGateModel+`  budget:
    limits: { hqc-credits: 10 }
`)
		if _, err := v.Admit(doc, "", FleetView{}); err == nil {
			t.Fatal("hqc-credits must be rejected on an empty fleet")
		}
		fleet := FleetView{BillingUnits: map[string]bool{"hqc-credits": true}}
		if _, err := v.Admit(doc, "", fleet); err != nil {
			t.Fatalf("fleet-declared unit must be accepted: %v", err)
		}
	})
}
