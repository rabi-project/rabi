// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SpecVersion is the wire-contract version this harness certifies against.
const SpecVersion = "v0.2"

// CategoryResult is one category's outcome in a certification run.
type CategoryResult struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
	Logs     []string `json:"logs,omitempty"`
}

// Report is the versioned certification artifact (M7). The JSON encoding of
// this struct is the signed document.
type Report struct {
	HarnessVersion string            `json:"harnessVersion"`
	SpecVersion    string            `json:"specVersion"`
	GeneratedAt    time.Time         `json:"generatedAt"`
	AdapterAddr    string            `json:"adapterAddr"`
	TargetID       string            `json:"targetId"`
	Capabilities   map[string]any    `json:"capabilities"`
	Categories     []*CategoryResult `json:"categories"`
	Passed         bool              `json:"passed"`
	// Notes carries run context a verifier should see (e.g. "fake-backend
	// mode"). Never used to soften failures.
	Notes []string `json:"notes,omitempty"`
}

// Recorder implements T, capturing category results for a Report instead of
// failing a *testing.T — the extraction point that makes the suite runnable
// from the rabi-conformance CLI.
type Recorder struct {
	Categories []*CategoryResult
}

type fatalSentinel struct{}

// Run records one category. Fatalf aborts only that category.
func (r *Recorder) Run(name string, f func(T)) bool {
	cat := &CategoryResult{Name: name, Passed: true}
	r.Categories = append(r.Categories, cat)
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatalSentinel); !ok {
					panic(v)
				}
			}
		}()
		f(&catT{cat: cat})
	}()
	return cat.Passed
}

// Helper/Fatalf/Errorf/Logf satisfy T at the top level (outside categories).
func (r *Recorder) Helper() {}
func (r *Recorder) Fatalf(format string, args ...any) {
	panic(fmt.Sprintf("conformance: fatal outside category: "+format, args...))
}
func (r *Recorder) Errorf(format string, args ...any) {
	panic(fmt.Sprintf("conformance: error outside category: "+format, args...))
}
func (r *Recorder) Logf(string, ...any) {}

// Passed reports whether every category passed.
func (r *Recorder) Passed() bool {
	for _, c := range r.Categories {
		if !c.Passed {
			return false
		}
	}
	return true
}

type catT struct{ cat *CategoryResult }

func (c *catT) Helper() {}
func (c *catT) Fatalf(format string, args ...any) {
	c.cat.Passed = false
	c.cat.Failures = append(c.cat.Failures, fmt.Sprintf(format, args...))
	panic(fatalSentinel{})
}
func (c *catT) Errorf(format string, args ...any) {
	c.cat.Passed = false
	c.cat.Failures = append(c.cat.Failures, fmt.Sprintf(format, args...))
}
func (c *catT) Logf(format string, args ...any) {
	c.cat.Logs = append(c.cat.Logs, fmt.Sprintf(format, args...))
}

// Sub-steps within a category share the category's result.
func (c *catT) Run(_ string, f func(T)) bool {
	f(c)
	return c.cat.Passed
}

// BuildReport assembles the artifact from a finished Recorder.
func BuildReport(harnessVersion, addr string, s *Suite, rec *Recorder, generatedAt time.Time, notes []string) *Report {
	caps := map[string]any{
		"numQubits":      s.Caps.GetNumQubits(),
		"programFormats": s.Caps.GetProgramFormats(),
		"maxShots":       s.Caps.GetMaxShots(),
		"sessions":       s.Caps.GetSessions(),
		"cancellation":   s.Caps.GetCancellation(),
		"billingUnits":   s.Caps.GetBillingUnits(),
		"technology":     s.Caps.GetTarget().GetTechnology(),
		"cloudQueue":     s.Caps.GetCloudQueue(),
		"vendor":         s.Caps.GetTarget().GetVendor(),
		"modality":       s.Caps.GetTarget().GetModality(),
		"simulator":      s.Caps.GetTarget().GetSimulator(),
	}
	return &Report{
		HarnessVersion: harnessVersion,
		SpecVersion:    SpecVersion,
		GeneratedAt:    generatedAt.UTC(),
		AdapterAddr:    addr,
		TargetID:       s.TargetID,
		Capabilities:   caps,
		Categories:     rec.Categories,
		Passed:         rec.Passed(),
		Notes:          notes,
	}
}

// CanonicalJSON is the byte sequence that gets signed.
func (r *Report) CanonicalJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Sign returns the ed25519 signature over the canonical JSON.
func (r *Report) Sign(key ed25519.PrivateKey) ([]byte, error) {
	doc, err := r.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(key, doc), nil
}

// Verify checks a signature against the canonical JSON.
func (r *Report) Verify(pub ed25519.PublicKey, sig []byte) bool {
	doc, err := r.CanonicalJSON()
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, doc, sig)
}

// Markdown renders the human-facing report.
func (r *Report) Markdown() string {
	var b strings.Builder
	verdict := "PASSED"
	if !r.Passed {
		verdict = "FAILED"
	}
	fmt.Fprintf(&b, "# Rabi adapter conformance report — %s\n\n", verdict)
	fmt.Fprintf(&b, "- Harness: %s (spec %s)\n- Generated: %s\n- Adapter: %s\n- Target: %s\n",
		r.HarnessVersion, r.SpecVersion, r.GeneratedAt.Format(time.RFC3339), r.AdapterAddr, r.TargetID)
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "- Note: %s\n", n)
	}
	b.WriteString("\n| Category | Result |\n|---|---|\n")
	for _, c := range r.Categories {
		res := "pass"
		if !c.Passed {
			res = "**FAIL**"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", c.Name, res)
	}
	for _, c := range r.Categories {
		if len(c.Failures) == 0 && len(c.Logs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n", c.Name)
		for _, f := range c.Failures {
			fmt.Fprintf(&b, "- FAIL: %s\n", f)
		}
		for _, l := range c.Logs {
			fmt.Fprintf(&b, "- log: %s\n", l)
		}
	}
	return b.String()
}
