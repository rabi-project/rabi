// SPDX-License-Identifier: Apache-2.0

// M7 harness self-test: intentionally broken adapter fixtures must fail
// exactly the right categories — a harness that cannot catch a planted bug
// certifies nothing.
package conformance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/rabi-project/rabi/internal/adaptertest"
)

func dialFake(t *testing.T, spec *adaptertest.TargetSpec) *Suite {
	t.Helper()
	fake := adaptertest.New(spec)
	addr := fake.Serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	suite, err := Dial(ctx, addr, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

func categoryResult(rec *Recorder, name string) *CategoryResult {
	for _, c := range rec.Categories {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSelfTestBrokenFixturesFailTheRightCategories(t *testing.T) {
	ctx := t.Context()

	// Control: an honest fake passes the full suite.
	honest := dialFake(t, &adaptertest.TargetSpec{
		ID: "honest", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 1000,
	})
	rec := &Recorder{}
	honest.Run(ctx, rec)
	if !rec.Passed() {
		for _, c := range rec.Categories {
			if !c.Passed {
				t.Errorf("honest fixture failed %s: %v", c.Name, c.Failures)
			}
		}
		t.Fatal("honest fixture must pass the full suite")
	}

	// Fixture 1: declares max_shots but accepts more → capability honesty
	// (cat 1) fails; everything else still passes.
	overShots := dialFake(t, &adaptertest.TargetSpec{
		ID: "overshots", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 1000,
		IgnoreMaxShots: true,
	})
	rec = &Recorder{}
	overShots.Run(ctx, rec)
	if c := categoryResult(rec, "cat1-capability-honesty"); c == nil || c.Passed {
		t.Fatalf("IgnoreMaxShots fixture must fail cat1, got %+v", c)
	}
	for _, c := range rec.Categories {
		if c.Name != "cat1-capability-honesty" && !c.Passed {
			t.Errorf("IgnoreMaxShots fixture leaked failure into %s: %v", c.Name, c.Failures)
		}
	}

	// Fixture 2: declares sessions but refuses OpenSession → cat 8 fails.
	brokenSess := dialFake(t, &adaptertest.TargetSpec{
		ID: "brokensess", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 1000,
		BrokenSessions: true,
	})
	rec = &Recorder{}
	brokenSess.Run(ctx, rec)
	if c := categoryResult(rec, "cat8-sessions"); c == nil || c.Passed {
		t.Fatalf("BrokenSessions fixture must fail cat8, got %+v", c)
	}
	for _, c := range rec.Categories {
		if c.Name != "cat8-sessions" && !c.Passed {
			t.Errorf("BrokenSessions fixture leaked failure into %s: %v", c.Name, c.Failures)
		}
	}
}

func TestReportSignVerifyAndRender(t *testing.T) {
	honest := dialFake(t, &adaptertest.TargetSpec{
		ID: "sign", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 1000,
	})
	rec := &Recorder{}
	honest.Run(t.Context(), rec)
	report := BuildReport("test-harness", "fake:0", honest, rec,
		time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC), []string{"self-test"})

	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := report.Sign(key)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Verify(pub, sig) {
		t.Fatal("signature does not verify")
	}
	report.Passed = !report.Passed // any mutation must break the signature
	if report.Verify(pub, sig) {
		t.Fatal("tampered report still verifies")
	}
	report.Passed = !report.Passed

	md := report.Markdown()
	for _, want := range []string{"PASSED", "cat1-capability-honesty", "spec v0.2", "self-test"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
