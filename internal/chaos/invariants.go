// SPDX-License-Identifier: Apache-2.0

// Package chaos is the Phase 2 chaos & invariants harness (P2.M1). It injects
// the eight test-plan §4 fault scenarios and, after each, asserts the five
// invariants that must hold no matter what broke:
//
//  1. no job lost      — every accepted job reaches a queryable state
//  2. no duplicate exec — the idempotency ledger admits one usage per task/unit
//  3. terminal immutable — no event follows a job's terminal transition
//  4. usage within caps — recorded usage never exceeds declared demand + tolerance
//  5. audit gapless     — each job's event chain is a legal, ordered FSM path
//
// The invariants operate on the store alone, so the same checks run in the CI
// component harness and in the --fleet0 game-day mode.
package chaos

import (
	"context"
	"fmt"
	"sort"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

// Violation is one broken invariant, named for the report.
type Violation struct {
	Invariant string
	JobID     string
	Detail    string
}

func (v Violation) String() string {
	if v.JobID != "" {
		return fmt.Sprintf("[%s] job %s: %s", v.Invariant, v.JobID, v.Detail)
	}
	return fmt.Sprintf("[%s] %s", v.Invariant, v.Detail)
}

// CheckAll runs every invariant over the accepted job ids and returns all
// violations (empty slice = healthy). accepted is every job id the harness
// successfully submitted; jobDeclaredShots maps job id → declared shots for
// the usage-cap check (0 = unknown, skipped).
func CheckAll(ctx context.Context, st *store.Store, accepted []string, jobDeclaredShots map[string]float64) []Violation {
	var vs []Violation
	vs = append(vs, noJobLost(ctx, st, accepted)...)
	vs = append(vs, noDuplicateExecution(ctx, st, accepted)...)
	vs = append(vs, terminalImmutable(ctx, st, accepted)...)
	vs = append(vs, usageWithinCaps(ctx, st, accepted, jobDeclaredShots)...)
	vs = append(vs, auditGapless(ctx, st, accepted)...)
	return vs
}

// 1. Every accepted job is queryable and in a known phase.
func noJobLost(ctx context.Context, st *store.Store, accepted []string) []Violation {
	var vs []Violation
	for _, id := range accepted {
		rec, err := st.GetJob(ctx, id)
		if err != nil {
			vs = append(vs, Violation{"no-job-lost", id, "not queryable: " + err.Error()})
			continue
		}
		if !rec.Phase.Valid() {
			vs = append(vs, Violation{"no-job-lost", id, "unknown phase " + string(rec.Phase)})
		}
	}
	return vs
}

// 2. The usage ledger holds at most one row per (task_id, unit) — no task's
// work is billed twice — and no job records more than one SUCCEEDED task.
func noDuplicateExecution(ctx context.Context, st *store.Store, accepted []string) []Violation {
	var vs []Violation
	rows, err := st.Pool.Query(ctx, `
		SELECT task_id, unit, count(*) FROM usage_ledger
		GROUP BY task_id, unit HAVING count(*) > 1`)
	if err != nil {
		return []Violation{{"no-duplicate-exec", "", "ledger scan: " + err.Error()}}
	}
	for rows.Next() {
		var task, unit string
		var n int64
		_ = rows.Scan(&task, &unit, &n)
		vs = append(vs, Violation{"no-duplicate-exec", "", fmt.Sprintf("task %s unit %s billed %d times", task, unit, n)})
	}
	rows.Close()

	for _, id := range accepted {
		var succeeded int64
		if err := st.Pool.QueryRow(ctx,
			`SELECT count(*) FROM tasks WHERE job_id = $1 AND state = 'SUCCEEDED'`, id).Scan(&succeeded); err != nil {
			continue
		}
		if succeeded > 1 {
			vs = append(vs, Violation{"no-duplicate-exec", id, fmt.Sprintf("%d SUCCEEDED tasks", succeeded)})
		}
	}
	return vs
}

// 3. Once a job reaches a terminal phase, no later event exists for it.
func terminalImmutable(ctx context.Context, st *store.Store, accepted []string) []Violation {
	var vs []Violation
	for _, id := range accepted {
		events, err := st.JobEventsSince(ctx, id, 0)
		if err != nil || len(events) == 0 {
			continue
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
		for i, e := range events {
			if e.Phase.Terminal() && i != len(events)-1 {
				vs = append(vs, Violation{"terminal-immutable", id,
					fmt.Sprintf("terminal %s at seq %d, but %d later event(s) follow", e.Phase, e.Seq, len(events)-1-i)})
				break
			}
		}
	}
	return vs
}

// 4. Recorded shots usage for a job never exceeds its declared shots plus a
// one-task tolerance (a single task may legitimately be double-counted at most
// across a resubmit; more is a leak or double execution).
func usageWithinCaps(ctx context.Context, st *store.Store, accepted []string, declared map[string]float64) []Violation {
	var vs []Violation
	for _, id := range accepted {
		capShots := declared[id]
		if capShots <= 0 {
			continue
		}
		var used float64
		if err := st.Pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(amount),0) FROM usage_ledger WHERE job_id = $1 AND unit = 'shots'`, id).Scan(&used); err != nil {
			continue
		}
		if used > capShots*1.0000001 { // exact-or-under; float slack only
			vs = append(vs, Violation{"usage-within-caps", id,
				fmt.Sprintf("recorded %.0f shots > declared %.0f", used, capShots)})
		}
	}
	return vs
}

// 5. Each job's event chain, ordered by seq, is a legal FSM path: it starts at
// PENDING and every consecutive transition is permitted. A missed or reordered
// transition (a gap) shows up as an illegal step.
func auditGapless(ctx context.Context, st *store.Store, accepted []string) []Violation {
	var vs []Violation
	for _, id := range accepted {
		events, err := st.JobEventsSince(ctx, id, 0)
		if err != nil || len(events) == 0 {
			continue
		}
		sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
		if events[0].Phase != job.Pending {
			vs = append(vs, Violation{"audit-gapless", id, "first event is " + string(events[0].Phase) + ", not PENDING"})
		}
		for i := 1; i < len(events); i++ {
			prev, cur := events[i-1].Phase, events[i].Phase
			if prev == cur {
				continue // condition-only status update, same phase — legal
			}
			if err := job.Transition(prev, cur); err != nil {
				vs = append(vs, Violation{"audit-gapless", id,
					fmt.Sprintf("illegal/reordered transition %s→%s at seq %d", prev, cur, events[i].Seq)})
			}
		}
	}
	return vs
}
