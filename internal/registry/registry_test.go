// SPDX-License-Identifier: Apache-2.0

package registry_test

import (
	"context"
	"testing"

	"tangle.dev/tangle/internal/adaptertest"
	"tangle.dev/tangle/internal/registry"
)

func TestEmptyRegistry(t *testing.T) {
	r := registry.New()
	targets, err := r.ListTargets(t.Context(), "")
	if err != nil || len(targets) != 0 {
		t.Fatalf("empty registry: %v, %v", targets, err)
	}
	if e := r.Entry("nope/nothing"); e != nil {
		t.Fatal("unknown entry must be nil")
	}
	tgt, err := r.GetTarget(t.Context(), "nope/nothing")
	if err != nil || tgt != nil {
		t.Fatalf("unknown target: %v, %v", tgt, err)
	}
	view := r.FleetView(t.Context())
	if len(view.ProgramFormats) != 0 || len(view.BillingUnits) != 0 {
		t.Fatalf("empty fleet view: %+v", view)
	}
	if r.AdapterClient("nope") != nil {
		t.Fatal("unknown site must have nil client")
	}
}

func TestNewFromSpecErrors(t *testing.T) {
	for _, spec := range []string{"garbage", "=addr", "name=", "a=b,,"} {
		if _, err := registry.NewFromSpec(spec); err == nil {
			t.Errorf("spec %q must be rejected", spec)
		}
	}
	if _, err := registry.NewFromSpec(""); err != nil {
		t.Errorf("empty spec is a valid empty fleet: %v", err)
	}
}

func TestDiscoveryAndFleetView(t *testing.T) {
	fake := adaptertest.New(
		&adaptertest.TargetSpec{ID: "alpha", Qubits: 5, Formats: []string{"openqasm3"}, SnapshotID: "snap-1"},
		&adaptertest.TargetSpec{ID: "beta", Qubits: 20, Formats: []string{"openqasm3", "qir"}, SnapshotID: "snap-2"},
	)
	addr := fake.Serve(t)

	r, err := registry.NewFromSpec("sim=" + addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)

	entries := r.Entries()
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Name != "sim/alpha" || entries[1].Name != "sim/beta" {
		t.Fatalf("entries misnamed: %s, %s", entries[0].Name, entries[1].Name)
	}
	if entries[0].State == nil || entries[0].State.GetCalibration().GetSnapshotId() != "snap-1" {
		t.Fatalf("state not polled: %+v", entries[0].State)
	}

	// API conversion carries capabilities and state as structs.
	tgt, err := r.GetTarget(ctx, "sim/beta")
	if err != nil || tgt == nil {
		t.Fatalf("GetTarget: %v %v", tgt, err)
	}
	caps := tgt.GetCapabilities().AsMap()
	if caps["numQubits"].(float64) != 20 {
		t.Fatalf("caps not mapped: %v", caps)
	}
	state := tgt.GetState().AsMap()
	if state["status"].(string) != "ONLINE" {
		t.Fatalf("state not mapped: %v", state)
	}

	// Modality filter.
	none, err := r.ListTargets(ctx, "annealing")
	if err != nil || len(none) != 0 {
		t.Fatalf("modality filter leaked: %v", none)
	}

	// Fleet view unions formats and units.
	view := r.FleetView(ctx)
	if !view.ProgramFormats["qir"] || !view.ProgramFormats["openqasm3"] || !view.BillingUnits["shots"] {
		t.Fatalf("fleet view wrong: %+v", view)
	}

	if r.AdapterClient("sim") == nil {
		t.Fatal("adapter client missing for configured site")
	}
}
