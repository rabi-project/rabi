// SPDX-License-Identifier: Apache-2.0

package status_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/status"
	"github.com/rabi-project/rabi/internal/store"
)

var testStore *store.Store

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	testStore, err = store.Open(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer testStore.Close()
	os.Exit(m.Run())
}

func TestGather_HealthyFromStore(t *testing.T) {
	ctx := context.Background()
	// A completed job (recent, terminal — not lost).
	rec := &store.JobRecord{
		JobID: uuid.NewString(), Tenant: "acme/qa", Name: "s", Phase: job.Succeeded,
		Doc: map[string]any{"spec": map[string]any{}}, Status: map[string]any{"phase": "SUCCEEDED"},
	}
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// A green game-day.
	now := time.Now()
	if err := testStore.RecordGameDay(ctx, store.GameDay{
		StartedAt: now.Add(-time.Minute), FinishedAt: now, Scenario: "invariant-sweep",
		Target: "compose", InvariantsGreen: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Gather "now" must be after the inserts (jobs get a DB-side created_at).
	gnow := time.Now().Add(time.Second)
	started := gnow.Add(-2 * time.Hour)
	d, err := status.Gather(ctx, testStore, started, gnow)
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if d.JobsLost != 0 || !d.NeverLost {
		t.Errorf("expected no lost jobs, got JobsLost=%d NeverLost=%v", d.JobsLost, d.NeverLost)
	}
	if d.UptimeDays <= 0 || d.OperationDays <= 0 {
		t.Errorf("uptime/operation should be positive: %+v", d)
	}
	if d.LastGameDay == nil || d.LastGameDay.Scenario != "invariant-sweep" {
		t.Errorf("last game-day not surfaced: %+v", d.LastGameDay)
	}
	if !d.Healthy {
		t.Errorf("should be healthy: %+v", d)
	}
}

func TestGather_LostJobDegrades(t *testing.T) {
	ctx := context.Background()
	// A non-terminal job created 48h ago = stuck/lost.
	old := time.Now().Add(-48 * time.Hour)
	id := uuid.NewString()
	rec := &store.JobRecord{
		JobID: id, Tenant: "acme/qa", Name: "stuck", Phase: job.Running,
		Doc: map[string]any{"spec": map[string]any{}}, Status: map[string]any{"phase": "RUNNING"},
	}
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.Pool.Exec(ctx, `UPDATE jobs SET created_at = $1 WHERE job_id = $2`, old, id); err != nil {
		t.Fatalf("age the job: %v", err)
	}
	d, err := status.Gather(ctx, testStore, time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if d.JobsLost == 0 || d.NeverLost {
		t.Errorf("a 48h non-terminal job should count as lost: %+v", d)
	}
	if d.Healthy {
		t.Error("a lost job must make the page report Degraded")
	}
}
