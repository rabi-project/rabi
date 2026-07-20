// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/rabi-project/rabi/internal/store"
)

func TestGameDays_RecordAndLast(t *testing.T) {
	ctx := context.Background()

	// No drill has run yet on a fresh table → ok=false, no error.
	if _, ok, err := testStore.LastGameDay(ctx); err != nil || ok {
		t.Fatalf("empty table: ok=%v err=%v (want ok=false, err=nil)", ok, err)
	}

	start := time.Now().Add(-90 * time.Second)
	green := store.GameDay{
		StartedAt: start, FinishedAt: start.Add(30 * time.Second),
		Scenario: "invariant-sweep", Target: "compose",
		InvariantsGreen: true, Violations: 0, Operator: "tester", Note: "first",
	}
	if err := testStore.RecordGameDay(ctx, green); err != nil {
		t.Fatalf("record green: %v", err)
	}

	// A later, red drill must become the "last" one (ordered by started_at).
	redStart := time.Now()
	red := store.GameDay{
		StartedAt: redStart, FinishedAt: redStart.Add(5 * time.Second),
		Scenario: "adapter-kill", Target: "fleet0",
		InvariantsGreen: false, Violations: 2, Operator: "tester", Note: "drill",
	}
	if err := testStore.RecordGameDay(ctx, red); err != nil {
		t.Fatalf("record red: %v", err)
	}

	got, ok, err := testStore.LastGameDay(ctx)
	if err != nil || !ok {
		t.Fatalf("last after records: ok=%v err=%v", ok, err)
	}
	if got.Scenario != "adapter-kill" || got.Target != "fleet0" {
		t.Errorf("latest drill = %s/%s, want adapter-kill/fleet0", got.Scenario, got.Target)
	}
	if got.InvariantsGreen || got.Violations != 2 {
		t.Errorf("latest result = green:%v violations:%d, want green:false violations:2", got.InvariantsGreen, got.Violations)
	}
	if got.Operator != "tester" || got.Note != "drill" {
		t.Errorf("latest metadata = %q/%q, want tester/drill", got.Operator, got.Note)
	}
}
