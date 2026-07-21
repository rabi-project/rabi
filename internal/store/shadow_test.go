// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rabi-project/rabi/internal/store"
)

func f64(v float64) *float64 { return &v }

func TestShadowPlacements_RoundTrip(t *testing.T) {
	ctx := context.Background()
	job := uuid.NewString()
	rec := store.ShadowPlacement{
		JobID: job, Tenant: "acme/qa", Policy: "cand/v9", ActivePolicy: "fifo/v0",
		ActiveTarget: "sim/a", ShadowTarget: "sim/b", Agreed: false,
		ActiveESP: f64(0.80), ShadowESP: f64(0.91), ActiveWait: f64(0), ShadowWait: f64(3),
	}
	if err := testStore.RecordShadowPlacement(ctx, rec); err != nil {
		t.Fatalf("record: %v", err)
	}
	// A no-feasible-target case records NULL proxies.
	if err := testStore.RecordShadowPlacement(ctx, store.ShadowPlacement{
		JobID: uuid.NewString(), Policy: "cand/v9", ActivePolicy: "fifo/v0", Agreed: true,
	}); err != nil {
		t.Fatalf("record null: %v", err)
	}

	got, err := testStore.ShadowPlacementsSince(ctx, "cand/v9", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("expected >= 2 rows, got %d", len(got))
	}
	var withProxy *store.ShadowPlacement
	for i := range got {
		if got[i].JobID == job {
			withProxy = &got[i]
		}
	}
	if withProxy == nil || withProxy.ShadowESP == nil || *withProxy.ShadowESP != 0.91 {
		t.Fatalf("proxy row not round-tripped: %+v", withProxy)
	}

	policies, err := testStore.ShadowPolicies(ctx)
	if err != nil {
		t.Fatalf("policies: %v", err)
	}
	found := false
	for _, p := range policies {
		if p == "cand/v9" {
			found = true
		}
	}
	if !found {
		t.Errorf("ShadowPolicies missing cand/v9: %v", policies)
	}
}
