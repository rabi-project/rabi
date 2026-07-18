// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rabi-project/rabi/internal/auth"
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
			if err == nil {
				admitted.Add(1)
				return
			}
			var qe *store.ErrQuotaExceeded
			if !errors.As(err, &qe) {
				t.Errorf("unexpected error kind: %v", err)
			}
			rejected.Add(1)
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

func TestQuotaHelpersAndWeights(t *testing.T) {
	ctx := t.Context()
	const tenant = "quota/helpers"
	if _, err := testStore.EnsureProject(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetProjectWeight(ctx, tenant, 4); err != nil {
		t.Fatal(err)
	}
	weights, err := testStore.ProjectWeights(ctx, []string{tenant, "never/created"})
	if err != nil {
		t.Fatal(err)
	}
	if weights[tenant] != 4 || weights["never/created"] != 1 {
		t.Fatalf("weights = %v", weights)
	}

	if err := testStore.SetQuota(ctx, tenant, "shots", 100); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "seconds", 60); err != nil {
		t.Fatal(err)
	}
	quotas, err := testStore.ListQuotas(ctx, tenant)
	if err != nil || len(quotas) != 2 {
		t.Fatalf("quotas = %v, %v", quotas, err)
	}
	// Update in place, then remove.
	if err := testStore.SetQuota(ctx, tenant, "shots", 200); err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetQuota(ctx, tenant, "seconds", -1); err != nil {
		t.Fatal(err)
	}
	quotas, err = testStore.ListQuotas(ctx, tenant)
	if err != nil || len(quotas) != 1 || quotas[0].Limit != 200 {
		t.Fatalf("after update/remove: %v, %v", quotas, err)
	}

	qe := &store.ErrQuotaExceeded{Unit: "shots", Limit: 10, Committed: 8, Requested: 5}
	if !strings.Contains(qe.Error(), "shots") || !strings.Contains(qe.Error(), "10") {
		t.Fatalf("error text uninformative: %s", qe.Error())
	}
}

func TestTouchTokenAndJobCondition(t *testing.T) {
	ctx := t.Context()
	_, id, hash, err := auth.MintToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.InsertToken(ctx, &store.TokenRecord{
		ID: id, Name: "touchable", Project: "helpers", Role: "viewer",
		TokenHash: hash, CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	testStore.TouchToken(ctx, id)
	rec, err := testStore.GetToken(ctx, id)
	if err != nil || rec.LastUsedAt == nil {
		t.Fatalf("touch not recorded: %v %+v", err, rec)
	}

	jobRec := shotsJob("helpers", 1)
	if err := testStore.InsertJob(ctx, jobRec); err != nil {
		t.Fatal(err)
	}
	changed, err := testStore.SetJobCondition(ctx, jobRec.JobID, map[string]any{
		"type": "Schedulable", "status": "False", "reason": "TestReason",
	})
	if err != nil || !changed {
		t.Fatalf("set condition: %v changed=%v", err, changed)
	}
	// Same condition again: no change recorded.
	changed, err = testStore.SetJobCondition(ctx, jobRec.JobID, map[string]any{
		"type": "Schedulable", "status": "False", "reason": "TestReason",
	})
	if err != nil || changed {
		t.Fatalf("condition dedup broken: %v changed=%v", err, changed)
	}
}

func TestStoreErrorPaths(t *testing.T) {
	ctx := t.Context()
	// Unknown job: condition set reports not-found.
	if _, err := testStore.SetJobCondition(ctx, uuid.NewString(), map[string]any{
		"type": "X", "status": "True", "reason": "Y",
	}); err == nil {
		t.Fatal("condition on missing job must error")
	}
	// Empty tenant is rejected before touching the database.
	if _, err := testStore.EnsureProject(ctx, ""); err == nil {
		t.Fatal("empty tenant must error")
	}
	// Archiving a nonexistent project reports found=false.
	if found, err := testStore.ArchiveProject(ctx, "ghost/project"); err != nil || found {
		t.Fatalf("ghost archive: %v found=%v", err, found)
	}
	// Revoking a nonexistent token reports found=false.
	if found, err := testStore.RevokeToken(ctx, "no-such-id"); err != nil || found {
		t.Fatalf("ghost revoke: %v found=%v", err, found)
	}
	// Duplicate job id: the quota insert surfaces the constraint violation.
	rec := shotsJob("helpers", 1)
	if err := testStore.InsertJobWithQuota(ctx, rec, nil); err != nil {
		t.Fatal(err)
	}
	if err := testStore.InsertJobWithQuota(ctx, rec, nil); err == nil {
		t.Fatal("duplicate job id must error")
	}
	// Duplicate token id: insert surfaces the constraint violation.
	_, id, hash, err := auth.MintToken()
	if err != nil {
		t.Fatal(err)
	}
	tok := &store.TokenRecord{ID: id, Name: "dup", Project: "helpers", Role: "viewer", TokenHash: hash, CreatedBy: "t"}
	if err := testStore.InsertToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := testStore.InsertToken(ctx, tok); err == nil {
		t.Fatal("duplicate token id must error")
	}
	// Invalid role violates the CHECK constraint.
	bad := &store.TokenRecord{ID: id + "x", Name: "bad", Project: "helpers", Role: "root", TokenHash: hash, CreatedBy: "t"}
	if err := testStore.InsertToken(ctx, bad); err == nil {
		t.Fatal("invalid role must be rejected by the schema")
	}
	// Audit decision outside allow|deny violates the CHECK constraint.
	if err := testStore.RecordAudit(ctx, store.AuditEntry{
		PrincipalType: "t", Subject: "s", Method: "m", Decision: "maybe",
	}); err == nil {
		t.Fatal("invalid audit decision must be rejected by the schema")
	}
}

func TestConditionReplaceAndDoubleBind(t *testing.T) {
	ctx := t.Context()
	rec := shotsJob("helpers", 1)
	if err := testStore.InsertJob(ctx, rec); err != nil {
		t.Fatal(err)
	}
	set := func(reason, msg string) bool {
		changed, err := testStore.SetJobCondition(ctx, rec.JobID, map[string]any{
			"type": "Schedulable", "status": "False", "reason": reason, "message": msg,
		})
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	if !set("NoFeasibleTarget", "0 of 3 targets") {
		t.Fatal("first condition must record")
	}
	if !set("NoFeasibleTarget", "1 of 3 targets") {
		t.Fatal("changed message must replace the same-type condition")
	}
	// A second condition type appends alongside.
	if changed, err := testStore.SetJobCondition(ctx, rec.JobID, map[string]any{
		"type": "QuotaPressure", "status": "True", "reason": "NearLimit",
	}); err != nil || !changed {
		t.Fatalf("second type: %v changed=%v", err, changed)
	}

	// Bind, then a second bind must refuse (job no longer PENDING).
	if _, err := testStore.BindJob(ctx, rec.JobID, uuid.NewString(), "site/t1", map[string]any{"policy": "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.BindJob(ctx, rec.JobID, uuid.NewString(), "site/t2", map[string]any{"policy": "test"}); err == nil {
		t.Fatal("double bind must fail")
	}
}

func TestUnfilteredListingsAndUsageWindow(t *testing.T) {
	ctx := t.Context()
	if _, err := testStore.ListTokens(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.AuditEntries(ctx, "", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.ListProjects(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.ListQuotas(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// Usage with an explicit window and with the zero-time defaults.
	if _, err := testStore.TenantUsage(ctx, "helpers", time.Time{}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.TenantUsage(ctx, "helpers",
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.GetJob(ctx, uuid.NewString()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing job: want ErrNotFound, got %v", err)
	}
}

func TestOpenAtErrorPaths(t *testing.T) {
	ctx := t.Context()
	if _, err := store.OpenAt(ctx, "postgres://%zz-not-a-url", 4); err == nil {
		t.Fatal("bad database url must error")
	}
}

// A closed pool exercises every query-error return in one sweep — these
// branches otherwise need fault injection.
func TestClosedPoolSurfacesErrors(t *testing.T) {
	ctx := t.Context()
	dead, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()

	if _, err := dead.GetProject(ctx, "x"); err == nil {
		t.Error("GetProject on closed pool must error")
	}
	if _, err := dead.ArchiveProject(ctx, "x"); err == nil {
		t.Error("ArchiveProject on closed pool must error")
	}
	if err := dead.SetQuota(ctx, "x", "shots", 1); err == nil {
		t.Error("SetQuota on closed pool must error")
	}
	if err := dead.SetQuota(ctx, "x", "shots", -1); err == nil {
		t.Error("SetQuota remove on closed pool must error")
	}
	if _, err := dead.RevokeToken(ctx, "x"); err == nil {
		t.Error("RevokeToken on closed pool must error")
	}
	if _, err := dead.ListQuotas(ctx, ""); err == nil {
		t.Error("ListQuotas on closed pool must error")
	}
	if _, err := dead.ProjectWeights(ctx, []string{"x"}); err == nil {
		t.Error("ProjectWeights on closed pool must error")
	}
	if _, err := dead.ListProjects(ctx, true); err == nil {
		t.Error("ListProjects on closed pool must error")
	}
	if _, err := dead.EnsureProject(ctx, "x"); err == nil {
		t.Error("EnsureProject on closed pool must error")
	}
	if err := dead.InsertToken(ctx, &store.TokenRecord{ID: "x"}); err == nil {
		t.Error("InsertToken on closed pool must error")
	}
	if _, err := dead.ListTokens(ctx, ""); err == nil {
		t.Error("ListTokens on closed pool must error")
	}
	if _, err := dead.GetToken(ctx, "x"); err == nil {
		t.Error("GetToken on closed pool must error")
	}
	if err := dead.RecordAudit(ctx, store.AuditEntry{Decision: "deny"}); err == nil {
		t.Error("RecordAudit on closed pool must error")
	}
	if _, err := dead.AuditEntries(ctx, "", 1); err == nil {
		t.Error("AuditEntries on closed pool must error")
	}
	if err := dead.InsertJobWithQuota(ctx, shotsJob("x", 1), nil); err == nil {
		t.Error("InsertJobWithQuota on closed pool must error")
	}
	if _, err := dead.BindJob(ctx, "x", "t", "tt", nil); err == nil {
		t.Error("BindJob on closed pool must error")
	}
}

func TestSessionStore(t *testing.T) {
	ctx := t.Context()
	exp := time.Now().Add(time.Hour)
	rec := &store.SessionRecord{
		SessionID: "s-" + uuid.NewString()[:8], Tenant: "sess/store", Target: "sim/t1",
		AdapterSessionID: "adapter-1", OpenedByJob: uuid.NewString(), ExpiresAt: &exp,
	}
	if err := testStore.InsertSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := testStore.GetSession(ctx, rec.SessionID)
	if err != nil || !got.Live(time.Now()) {
		t.Fatalf("fresh session: %v live=%v", err, got.Live(time.Now()))
	}
	if !got.Live(exp.Add(-time.Second)) || got.Live(exp.Add(time.Second)) {
		t.Fatal("expiry boundary wrong")
	}
	if err := testStore.CloseSession(ctx, rec.SessionID); err != nil {
		t.Fatal(err)
	}
	got, _ = testStore.GetSession(ctx, rec.SessionID)
	if got.Live(time.Now()) {
		t.Fatal("closed session still live")
	}
	if _, err := testStore.GetSession(ctx, "missing"); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}
