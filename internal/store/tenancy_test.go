// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

func shotsJob(tenant string, shots float64) *store.JobRecord {
	return &store.JobRecord{
		JobID:  uuid.NewString(),
		Tenant: tenant,
		Name:   "quota-" + uuid.NewString()[:8],
		Phase:  job.Pending,
		Doc: map[string]any{
			"spec": map[string]any{
				"workload": map[string]any{
					"kind":      "gate-model",
					"gateModel": map[string]any{"shots": shots},
				},
			},
		},
		Status: map[string]any{"phase": "PENDING"},
	}
}

func TestProjectLifecycle(t *testing.T) {
	ctx := t.Context()
	p, err := testStore.EnsureProject(ctx, "orbital/lab-7")
	if err != nil {
		t.Fatal(err)
	}
	if p.Org != "orbital" || p.Name != "lab-7" || p.Weight != 1 {
		t.Fatalf("derived fields wrong: %+v", p)
	}
	// Bare tenant strings derive project "default".
	bare, err := testStore.EnsureProject(ctx, "soloco")
	if err != nil {
		t.Fatal(err)
	}
	if bare.Org != "soloco" || bare.Name != "default" {
		t.Fatalf("bare tenant mapping wrong: %+v", bare)
	}
	// EnsureProject is idempotent.
	again, err := testStore.EnsureProject(ctx, "orbital/lab-7")
	if err != nil || !again.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("EnsureProject not idempotent: %v %+v", err, again)
	}

	if found, err := testStore.ArchiveProject(ctx, "orbital/lab-7"); err != nil || !found {
		t.Fatalf("archive: %v %v", err, found)
	}
	got, _ := testStore.GetProject(ctx, "orbital/lab-7")
	if got.ArchivedAt == nil {
		t.Fatal("archive did not stick")
	}
	active, err := testStore.ListProjects(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range active {
		if pr.Tenant == "orbital/lab-7" {
			t.Fatal("archived project listed as active")
		}
	}

	if err := testStore.SetProjectWeight(ctx, "soloco", 3); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetProjectWeight(ctx, "missing", 3); !errors.Is(err, store.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestQuotaAdmission(t *testing.T) {
	ctx := t.Context()
	const tenant = "quota/basic"
	if _, err := testStore.EnsureProject(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "shots", 2500); err != nil {
		t.Fatal(err)
	}

	// 1000 + 1000 fit; the third 1000 must be rejected with the typed error.
	for i := 0; i < 2; i++ {
		if err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 1000), map[string]float64{"shots": 1000}); err != nil {
			t.Fatalf("submission %d within quota rejected: %v", i, err)
		}
	}
	err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 1000), map[string]float64{"shots": 1000})
	var qe *store.ErrQuotaExceeded
	if !errors.As(err, &qe) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	if qe.Unit != "shots" || qe.Committed != 2000 || qe.Limit != 2500 {
		t.Fatalf("quota error detail wrong: %+v", qe)
	}
	// A smaller job that still fits is admitted (500 remaining).
	if err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 500), map[string]float64{"shots": 500}); err != nil {
		t.Fatalf("fitting job rejected: %v", err)
	}
	// Units without quota rows are unlimited.
	if err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 0), map[string]float64{"seconds": 1e9}); err != nil {
		t.Fatalf("unquota'd unit rejected: %v", err)
	}
}

// The §3 race criterion: 100 concurrent submissions against a quota with
// room for exactly 7 admit exactly 7.
func TestQuotaRace(t *testing.T) {
	ctx := t.Context()
	const tenant = "quota/race"
	if _, err := testStore.EnsureProject(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "shots", 7_000); err != nil {
		t.Fatal(err)
	}

	var admitted, rejected atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 1000), map[string]float64{"shots": 1000})
			switch {
			case err == nil:
				admitted.Add(1)
			default:
				var qe *store.ErrQuotaExceeded
				if !errors.As(err, &qe) {
					t.Errorf("unexpected error kind: %v", err)
				}
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 7 || rejected.Load() != 93 {
		t.Fatalf("admitted %d / rejected %d; want exactly 7 / 93", admitted.Load(), rejected.Load())
	}
}

// Terminal jobs must release their declared reservation (their usage lives
// in the ledger instead — no double counting, no leaked reservation).
func TestQuotaReservationReleasedOnTerminal(t *testing.T) {
	ctx := t.Context()
	const tenant = "quota/release"
	if _, err := testStore.EnsureProject(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "shots", 1000); err != nil {
		t.Fatal(err)
	}
	first := shotsJob(tenant, 1000)
	if err := testStore.InsertJobWithQuota(ctx, first, map[string]float64{"shots": 1000}); err != nil {
		t.Fatal(err)
	}
	// Full: the next submission bounces.
	if err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 1000), map[string]float64{"shots": 1000}); err == nil {
		t.Fatal("over-quota submission admitted")
	}
	// Cancel the first job (terminal, no ledger rows) — reservation released.
	if _, err := testStore.TransitionJob(ctx, first.JobID, job.Cancelled, nil); err != nil {
		t.Fatal(err)
	}
	if err := testStore.InsertJobWithQuota(ctx, shotsJob(tenant, 1000), map[string]float64{"shots": 1000}); err != nil {
		t.Fatalf("reservation not released on terminal phase: %v", err)
	}
}

// The §3 migration criterion, staged for real: a database at the Phase-0
// schema (version 4) with live tenant traffic is migrated forward; every
// tenant string gets a project row and no data is lost.
func TestPhase0DatabaseUpgradesZeroLoss(t *testing.T) {
	ctx := t.Context()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	// Stage 1: schema version 4 (pre-tenancy), seeded the Phase-0 way.
	old, err := store.OpenAt(ctx, dsn, 4)
	if err != nil {
		t.Fatal(err)
	}
	tenants := []string{"acme/qa", "legacy-solo", "acme/prod"}
	for i, tenant := range tenants {
		rec := shotsJob(tenant, 10)
		rec.Name = fmt.Sprintf("phase0-%d", i)
		if err := old.InsertJob(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Pool.Exec(ctx, `
		INSERT INTO api_tokens (id, name, project, role, token_hash, created_by)
		VALUES ('deadbeef0000', 'legacy', 'token-only/proj', 'member', 'x', 'test')`); err != nil {
		t.Fatal(err)
	}
	var jobsBefore int64
	if err := old.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobsBefore); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// Stage 2: the upgrade a site performs — Open() migrates to head.
	upgraded, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer upgraded.Close()

	var jobsAfter int64
	if err := upgraded.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&jobsAfter); err != nil {
		t.Fatal(err)
	}
	if jobsAfter != jobsBefore {
		t.Fatalf("job rows changed across upgrade: %d → %d", jobsBefore, jobsAfter)
	}
	expect := map[string][2]string{
		"acme/qa":         {"acme", "qa"},
		"legacy-solo":     {"legacy-solo", "default"},
		"acme/prod":       {"acme", "prod"},
		"token-only/proj": {"token-only", "proj"},
	}
	for tenant, want := range expect {
		p, err := upgraded.GetProject(ctx, tenant)
		if err != nil {
			t.Fatalf("tenant %q has no project after upgrade: %v", tenant, err)
		}
		if p.Org != want[0] || p.Name != want[1] {
			t.Fatalf("tenant %q mapped to %s/%s, want %s/%s", tenant, p.Org, p.Name, want[0], want[1])
		}
	}
}

// §3 property: usage never exceeds quota (strict — declared-reservation
// admission needs no one-task tolerance). Seeded stdlib rand per D-023.
func TestQuotaNeverExceededProperty(t *testing.T) {
	ctx := t.Context()
	const tenant = "quota/property"
	const limit = 10_000.0
	if _, err := testStore.EnsureProject(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "shots", limit); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20260719))
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		shots := float64(rng.Intn(2000) + 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = testStore.InsertJobWithQuota(ctx, shotsJob(tenant, shots), map[string]float64{"shots": shots})
		}()
	}
	wg.Wait()
	var committed float64
	if err := testStore.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(declared_cost(doc, 'shots')), 0) FROM jobs
		WHERE tenant = $1 AND phase NOT IN ('SUCCEEDED','FAILED','CANCELLED')`,
		tenant).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	if committed > limit {
		t.Fatalf("committed %.0f exceeds quota %.0f", committed, limit)
	}
	if committed == 0 {
		t.Fatal("property run admitted nothing; vacuous")
	}
}

// Org/project derivation from the wire tenant string, table-tested
// (§3 "org/project inheritance"): a project inherits its org from the
// string's first segment; richer org-entity policies arrive when something
// consumes them (D-036).
func TestTenantDerivationTable(t *testing.T) {
	ctx := t.Context()
	cases := []struct{ tenant, org, name string }{
		{"acme/qa", "acme", "qa"},
		{"acme/sub/team-x", "acme", "sub/team-x"},
		{"bare", "bare", "default"},
		{"trailing/", "trailing", ""},
	}
	for _, c := range cases {
		p, err := testStore.EnsureProject(ctx, c.tenant)
		if err != nil {
			t.Fatalf("EnsureProject(%q): %v", c.tenant, err)
		}
		if p.Org != c.org || p.Name != c.name {
			t.Errorf("tenant %q → %s/%s, want %s/%s", c.tenant, p.Org, p.Name, c.org, c.name)
		}
	}
}
