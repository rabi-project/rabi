// SPDX-License-Identifier: Apache-2.0

package upgrade_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

const rehearsalToken = "upgrade-rehearsal-token"

// MaxUpgradeUnavailability is the test-plan §4 bound: API downtime during an
// N-1 -> N control-plane roll must stay under 30s.
const MaxUpgradeUnavailability = 30 * time.Second

// plane is one running control plane (store + dispatcher + API) over a shared,
// persistent adapter and database — what a single rabi process is.
type plane struct {
	store    *store.Store
	grpcAddr string
	cancel   context.CancelFunc
}

func bootPlane(t *testing.T, ctx context.Context, dsn, adapterAddr string) *plane {
	t.Helper()
	// store.Open runs any pending migrations — the real upgrade's migrate step.
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := registry.NewFromSpec("synth=" + adapterAddr)
	if err != nil {
		st.Close()
		cancel()
		t.Fatalf("registry: %v", err)
	}
	reg.Start(runCtx)
	d, err := dispatch.New(st, reg, "", logger)
	if err != nil {
		st.Close()
		cancel()
		t.Fatalf("dispatch: %v", err)
	}
	go d.Run(runCtx)
	validator, err := job.NewValidator()
	if err != nil {
		st.Close()
		cancel()
		t.Fatal(err)
	}
	authn, err := api.NewAuthenticator(rehearsalToken, nil, st, logger)
	if err != nil {
		st.Close()
		cancel()
		t.Fatal(err)
	}
	srv, err := api.New(api.Config{
		GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0",
		Auth: authn, Registry: reg, Fleet: reg, Store: st, Validator: validator, Canceller: d,
	})
	if err != nil {
		st.Close()
		cancel()
		t.Fatalf("api: %v", err)
	}
	go func() { _ = srv.Run(runCtx) }()
	return &plane{store: st, grpcAddr: srv.GRPCAddr(), cancel: cancel}
}

func (p *plane) stop() {
	p.cancel()
	p.store.Close()
}

// apiReadable returns true if the plane serves a read right now.
func (p *plane) apiReadable(ctx context.Context) bool {
	conn, err := grpc.NewClient(p.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cctx = metadata.AppendToOutgoingContext(cctx, "authorization", "Bearer "+rehearsalToken)
	_, err = apiv1alpha1.NewJobsServiceClient(conn).ListJobs(cctx, &apiv1alpha1.ListJobsRequest{PageSize: 1})
	return err == nil
}

// TestUpgradeRehearsal rehearses an N-1 -> N control-plane roll under live load:
// jobs are in flight when the plane is torn down, migrations run, and a fresh
// plane comes up against the same database and the same (still-running) adapter.
// It asserts zero jobs lost, API unavailability under the bound, and that jobs
// genuinely were in flight across the cut (otherwise the test proves nothing).
func TestUpgradeRehearsal(t *testing.T) {
	ctx := context.Background()

	// The adapter persists across the upgrade — only the control plane rolls.
	// A slow-ish step keeps jobs in flight across the cut.
	fake := adaptertest.New(&adaptertest.TargetSpec{
		ID: "t1", Qubits: 8, Formats: []string{"openqasm3"}, MaxShots: 100000,
		StepDelay: 300 * time.Millisecond,
	})
	addr, stopFake, err := fake.ServeControllable("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stopFake()

	dsn := freshDB(t)
	const jobs = 60

	// --- N-1 plane: submit a backlog and let work get genuinely in flight. ---
	p1 := bootPlane(t, ctx, dsn, addr)
	for i := 0; i < jobs; i++ {
		seedRehearsalJob(t, ctx, p1.store)
	}
	// Wait until jobs are actually executing on the adapter (SUBMITTED/RUNNING),
	// not merely PENDING — that is what makes resume() the thing under test.
	var running int64
	deadlineIF := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadlineIF) {
		if running = countRunning(t, ctx, dsn); running >= 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if running == 0 {
		t.Fatal("no jobs reached the adapter before the upgrade; rehearsal would prove nothing")
	}
	t.Logf("upgrade begins with %d job(s) executing on the adapter", running)

	// --- The upgrade: stop N-1, run migrations + start N. ---
	t0 := time.Now()
	p1.stop()
	p2 := bootPlane(t, ctx, dsn, addr) // store.Open re-runs migrations (none pending)
	// Measure API unavailability: time until the new plane serves a read.
	for !p2.apiReadable(ctx) {
		if time.Since(t0) > MaxUpgradeUnavailability {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	unavailable := time.Since(t0)
	defer p2.stop()
	t.Logf("API unavailability during roll: %v", unavailable.Round(time.Millisecond))

	// --- Everything must drain to terminal on the new plane. ---
	deadline := time.Now().Add(90 * time.Second)
	var nonTerminal, total int64
	for time.Now().Before(deadline) {
		nonTerminal = countNonTerminal(t, ctx, dsn)
		total = countAllJobs(t, ctx, dsn)
		if nonTerminal == 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if total != jobs {
		t.Errorf("job count changed across upgrade: submitted %d, found %d (lost jobs!)", jobs, total)
	}
	if nonTerminal != 0 {
		t.Errorf("%d job(s) never reached terminal after the upgrade", nonTerminal)
	}
	if failed := countByPhase(t, ctx, dsn, "FAILED"); failed != 0 {
		t.Errorf("%d job(s) FAILED across the upgrade (should be zero attributable to the roll)", failed)
	}
	if unavailable > MaxUpgradeUnavailability {
		t.Errorf("API unavailability %v exceeded bound %v", unavailable, MaxUpgradeUnavailability)
	}
	succeeded := countByPhase(t, ctx, dsn, "SUCCEEDED")
	t.Logf("post-upgrade: %d/%d SUCCEEDED, unavailability %v", succeeded, jobs, unavailable.Round(time.Millisecond))
}

func seedRehearsalJob(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	id := uuid.NewString()
	inline := base64.StdEncoding.EncodeToString([]byte("OPENQASM 3.0;\nqubit[2] q;\n"))
	rec := &store.JobRecord{
		JobID: id, Tenant: "acme/qa", Name: "rehearsal", Phase: job.Pending,
		Doc: map[string]any{
			"apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
			"metadata": map[string]any{"name": "rehearsal", "tenant": "acme/qa"},
			"spec": map[string]any{
				"workload": map[string]any{"kind": "gate-model", "gateModel": map[string]any{
					"program": map[string]any{"format": "openqasm3", "inline": inline},
					"shots":   float64(1000),
				}},
				"requirements": map[string]any{"qubits": float64(2)},
			},
		},
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	if err := st.InsertJob(ctx, rec); err != nil {
		t.Fatalf("seed job: %v", err)
	}
}

func countNonTerminal(t *testing.T, ctx context.Context, dsn string) int64 {
	return phaseCount(t, ctx, dsn, `phase NOT IN ('SUCCEEDED','FAILED','CANCELLED')`)
}
func countRunning(t *testing.T, ctx context.Context, dsn string) int64 {
	return phaseCount(t, ctx, dsn, `phase IN ('SUBMITTED','RUNNING')`)
}
func countAllJobs(t *testing.T, ctx context.Context, dsn string) int64 {
	return phaseCount(t, ctx, dsn, "TRUE")
}
func countByPhase(t *testing.T, ctx context.Context, dsn, phase string) int64 {
	return phaseCount(t, ctx, dsn, "phase = '"+phase+"'")
}

func phaseCount(t *testing.T, ctx context.Context, dsn, pred string) int64 {
	t.Helper()
	st, err := store.OpenNoMigrate(ctx, dsn)
	if err != nil {
		t.Fatalf("count connect: %v", err)
	}
	defer st.Close()
	var n int64
	if err := st.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE "+pred).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
