// SPDX-License-Identifier: Apache-2.0

package dispatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rabi-project/rabi/internal/store"
)

func pendingFor(tenant string, n int, base time.Time) []*store.JobRecord {
	out := make([]*store.JobRecord, n)
	for i := range out {
		out[i] = &store.JobRecord{
			JobID:     fmt.Sprintf("%s-%02d", tenant, i),
			Tenant:    tenant,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	return out
}

func cadence(order []*store.JobRecord) string {
	var b strings.Builder
	for _, rec := range order {
		b.WriteString(rec.Tenant[:1])
	}
	return b.String()
}

// The §3 fair-share golden: two projects at 3:1 weights under contention
// bind in an exact 3:1 cadence, FIFO within each project. Deterministic —
// any change to the ordering rule must update this golden consciously.
func TestFairShareGolden31(t *testing.T) {
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	// Interleave arrival order (oldest-first, alternating) like PendingJobs.
	a := pendingFor("alpha/sim", 12, base)
	b := pendingFor("beta/sim", 12, base.Add(500*time.Millisecond))
	var pending []*store.JobRecord
	for i := range a {
		pending = append(pending, a[i], b[i])
	}

	order := fairOrder(pending, map[string]int{"alpha/sim": 3, "beta/sim": 1})
	if len(order) != 24 {
		t.Fatalf("ordering dropped jobs: %d/24", len(order))
	}
	const golden = "aaabaaabaaabaaabbbbbbbbb" // alpha drains 3:1, beta finishes the tail
	if got := cadence(order); got != golden {
		t.Fatalf("cadence = %s, want %s", got, golden)
	}
	// FIFO within each project.
	seen := map[string]int{}
	for _, rec := range order {
		var idx int
		_, _ = fmt.Sscanf(rec.JobID[strings.LastIndex(rec.JobID, "-")+1:], "%d", &idx)
		if idx != seen[rec.Tenant] {
			t.Fatalf("intra-project FIFO broken at %s (want index %d)", rec.JobID, seen[rec.Tenant])
		}
		seen[rec.Tenant]++
	}
}

func TestFairOrderDefaultsAndEdges(t *testing.T) {
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	// Unknown weights default to 1 → strict alternation by deficit with
	// lexical tie-break.
	var pending []*store.JobRecord
	pending = append(pending, pendingFor("x", 3, base)...)
	pending = append(pending, pendingFor("y", 3, base)...)
	order := fairOrder(pending, map[string]int{})
	if got := cadence(order); got != "xyxyxy" {
		t.Fatalf("equal-weight cadence = %s, want xyxyxy", got)
	}
	// Single tenant and empty input pass through untouched.
	solo := pendingFor("solo", 2, base)
	if got := fairOrder(solo, nil); len(got) != 2 || got[0].JobID != "solo-00" {
		t.Fatalf("single-tenant order mangled: %+v", got)
	}
	if got := fairOrder(nil, nil); len(got) != 0 {
		t.Fatalf("empty input: %v", got)
	}
}
