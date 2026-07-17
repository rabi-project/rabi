// SPDX-License-Identifier: Apache-2.0

// Component tests for the store against a real Postgres (testcontainers).
package store_test

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mAengo31/rabi/internal/job"
	"github.com/mAengo31/rabi/internal/store"
)

var (
	testDSN   string
	testStore *store.Store
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("tangle"),
		tcpostgres.WithUsername("tangle"),
		tcpostgres.WithPassword("tangle"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("starting postgres container: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()

	testDSN, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("dsn: %v", err)
	}
	testStore, err = store.Open(ctx, testDSN)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer testStore.Close()

	os.Exit(m.Run())
}

func newRec(id, tenant, name string) *store.JobRecord {
	return &store.JobRecord{
		JobID:  id,
		Tenant: tenant,
		Name:   name,
		Phase:  job.Pending,
		Doc:    map[string]any{"metadata": map[string]any{"name": name, "tenant": tenant}},
		Status: map[string]any{"phase": string(job.Pending)},
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := store.Open(t.Context(), "not a url ::"); err == nil {
		t.Fatal("expected parse error")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Open(cancelled, "postgres://nobody@127.0.0.1:1/void"); err == nil {
		t.Fatal("expected unreachable-database error")
	}
}

// Open must be idempotent across restarts: migrations already applied is fine.
func TestOpenTwice(t *testing.T) {
	st2, err := store.Open(t.Context(), testDSN)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	st2.Close()
}

func TestInsertAndGet(t *testing.T) {
	ctx := t.Context()
	rec := newRec("11111111-1111-1111-1111-111111111111", "acme", "insert-get")
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if rec.CreatedAt.IsZero() || rec.UpdatedAt.IsZero() {
		t.Fatal("insert did not backfill timestamps")
	}

	got, err := testStore.GetJob(ctx, rec.JobID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Tenant != "acme" || got.Name != "insert-get" || got.Phase != job.Pending {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Duplicate id must fail (and leave no partial event behind).
	if err := testStore.InsertJob(ctx, rec); err == nil {
		t.Fatal("duplicate insert must fail")
	}
	events, err := testStore.JobEventsSince(ctx, rec.JobID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 admission event, got %d", len(events))
	}
}

func TestInsertMarshalError(t *testing.T) {
	rec := newRec("22222222-2222-2222-2222-222222222222", "acme", "bad-doc")
	rec.Doc = map[string]any{"bad": make(chan int)}
	if err := testStore.InsertJob(t.Context(), rec); err == nil {
		t.Fatal("unmarshalable doc must fail")
	}
	rec.Doc = map[string]any{}
	rec.Status = map[string]any{"bad": make(chan int)}
	if err := testStore.InsertJob(t.Context(), rec); err == nil {
		t.Fatal("unmarshalable status must fail")
	}
}

func TestGetNotFound(t *testing.T) {
	_, err := testStore.GetJob(t.Context(), "99999999-9999-9999-9999-999999999999")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTransitionLifecycleAndErrors(t *testing.T) {
	ctx := t.Context()
	rec := newRec("33333333-3333-3333-3333-333333333333", "acme", "transitions")
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Illegal jump PENDING → RUNNING is refused by the FSM inside the lock.
	if _, err := testStore.TransitionJob(ctx, rec.JobID, job.Running, nil); err == nil ||
		!strings.Contains(err.Error(), "illegal transition") {
		t.Fatalf("expected illegal-transition error, got %v", err)
	}

	// Full legal walk, with a status mutation along the way.
	for _, next := range []job.Phase{job.Scheduled, job.Submitted, job.Running, job.Succeeded} {
		got, err := testStore.TransitionJob(ctx, rec.JobID, next, func(st map[string]any) map[string]any {
			st["lastStep"] = string(next)
			return st
		})
		if err != nil {
			t.Fatalf("transition to %s: %v", next, err)
		}
		if got.Phase != next || got.Status["phase"] != string(next) || got.Status["lastStep"] != string(next) {
			t.Fatalf("transition state mismatch: %+v", got)
		}
	}

	// Terminal jobs are immutable.
	if _, err := testStore.TransitionJob(ctx, rec.JobID, job.Cancelled, nil); err == nil {
		t.Fatal("terminal job must refuse transitions")
	}

	// Unknown job.
	if _, err := testStore.TransitionJob(ctx, "99999999-9999-9999-9999-999999999999", job.Cancelled, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	// Event history: admission + 4 transitions, in order.
	events, err := testStore.JobEventsSince(ctx, rec.JobID, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantPhases := []job.Phase{job.Pending, job.Scheduled, job.Submitted, job.Running, job.Succeeded}
	if len(events) != len(wantPhases) {
		t.Fatalf("got %d events, want %d", len(events), len(wantPhases))
	}
	for i, ev := range events {
		if ev.Phase != wantPhases[i] {
			t.Fatalf("event %d phase = %s, want %s", i, ev.Phase, wantPhases[i])
		}
		if i > 0 && events[i].Seq <= events[i-1].Seq {
			t.Fatal("event seq not monotonic")
		}
	}

	// Since-filtering returns only later events.
	tail, err := testStore.JobEventsSince(ctx, rec.JobID, events[2].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 || tail[0].Phase != job.Running {
		t.Fatalf("since-filter wrong: %+v", tail)
	}

	// A nil-status mutate still records the phase.
	rec2 := newRec("44444444-4444-4444-4444-444444444444", "acme", "nil-mutate")
	rec2.Status = nil
	if err := testStore.InsertJob(ctx, rec2); err == nil {
		// nil status marshals to "null" jsonb — accept either behavior, but the
		// record must stay consistent. Reload to confirm.
		if _, err := testStore.GetJob(ctx, rec2.JobID); err != nil {
			t.Fatalf("reload after nil-status insert: %v", err)
		}
	}
}

func TestListJobsPaging(t *testing.T) {
	ctx := t.Context()
	ids := []string{
		"55555555-5555-5555-5555-555555555501",
		"55555555-5555-5555-5555-555555555502",
		"55555555-5555-5555-5555-555555555503",
	}
	for i, id := range ids {
		if err := testStore.InsertJob(ctx, newRec(id, "pager", string(rune('a'+i))+"-job")); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := testStore.ListJobs(ctx, "pager", "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	page2, err := testStore.ListJobs(ctx, "pager", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || len(page2) != 1 {
		t.Fatalf("paging sizes: %d, %d", len(page1), len(page2))
	}
	if _, err := testStore.ListJobs(ctx, "pager", string(job.Succeeded), 10, 0); err != nil {
		t.Fatal(err)
	}
}

// Every repository method must surface infrastructure failures as errors,
// never panics or silent nils — exercised via a closed pool.
func TestClosedPoolErrors(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	rec := newRec("66666666-6666-6666-6666-666666666666", "acme", "closed")
	if err := st.InsertJob(ctx, rec); err == nil {
		t.Fatal("InsertJob on closed pool must fail")
	}
	if _, err := st.GetJob(ctx, rec.JobID); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetJob on closed pool must fail with a non-NotFound error, got %v", err)
	}
	if _, err := st.ListJobs(ctx, "", "", 10, 0); err == nil {
		t.Fatal("ListJobs on closed pool must fail")
	}
	if _, err := st.TransitionJob(ctx, rec.JobID, job.Cancelled, nil); err == nil {
		t.Fatal("TransitionJob on closed pool must fail")
	}
	if _, err := st.JobEventsSince(ctx, rec.JobID, 0); err == nil {
		t.Fatal("JobEventsSince on closed pool must fail")
	}
	if _, err := st.CountRows(ctx, "jobs"); err == nil {
		t.Fatal("CountRows on closed pool must fail")
	}
}

// A mutate hook returning an unmarshalable status must abort the transition.
func TestTransitionMarshalError(t *testing.T) {
	ctx := t.Context()
	rec := newRec("77777777-7777-7777-7777-777777777777", "acme", "bad-mutate")
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	_, err := testStore.TransitionJob(ctx, rec.JobID, job.Scheduled, func(map[string]any) map[string]any {
		return map[string]any{"bad": make(chan int)}
	})
	if err == nil {
		t.Fatal("unmarshalable status must fail the transition")
	}
	got, err := testStore.GetJob(ctx, rec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != job.Pending {
		t.Fatalf("failed transition must not change phase; got %s", got.Phase)
	}
}

func TestCountRowsGuard(t *testing.T) {
	if _, err := testStore.CountRows(t.Context(), "pg_catalog.pg_tables"); err == nil {
		t.Fatal("unknown table must be refused")
	}
	if _, err := testStore.CountRows(t.Context(), "jobs"); err != nil {
		t.Fatal(err)
	}
}
