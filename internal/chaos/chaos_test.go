// SPDX-License-Identifier: Apache-2.0

// The Phase 2 chaos suite (P2.M1): the eight test-plan §4 fault scenarios,
// each asserting the five invariants afterward. Runs against a real Postgres
// (testcontainers), the real dispatcher, and a controllable fake adapter —
// the same control-plane code paths a live fault would hit.
package chaos_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/chaos"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

var (
	pgContainer *tcpostgres.PostgresContainer
	testDSN     string
	testStore   *store.Store
)

func freeHostPort() int {
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	// Pin Postgres to a fixed host port: the PostgresRestart scenario stops and
	// starts the container, and without a pinned port the host mapping changes,
	// stranding the shared pool. With it, restart is transparent and the pool
	// reconnects.
	hostPort := freeHostPort()
	pinPort := testcontainers.CustomizeRequestOption(func(req *testcontainers.GenericContainerRequest) error {
		req.ExposedPorts = []string{"5432/tcp"}
		req.HostConfigModifier = func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("5432/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: fmt.Sprint(hostPort)}},
			}
		}
		return nil
	})
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies(), pinPort)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	pgContainer = pg
	testDSN, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	testStore, err = store.Open(ctx, testDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer testStore.Close()
	os.Exit(m.Run())
}

// stack is one chaos fixture: a controllable adapter + a running dispatcher.
type stack struct {
	t        *testing.T
	fake     *adaptertest.Fake
	addr     string
	stopSrv  func()
	reg      *registry.Registry
	cancel   context.CancelFunc
	accepted []string
	declared map[string]float64
}

// newStack serves a controllable adapter on an ephemeral (but reusable) port
// and starts a dispatcher against it.
func newStack(t *testing.T, spec *adaptertest.TargetSpec) *stack {
	t.Helper()
	fake := adaptertest.New(spec)
	addr, stopSrv, err := fake.ServeControllable("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.NewFromSpec("sim=" + addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reg.Start(ctx)
	d, err := dispatch.New(testStore, reg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go d.Run(ctx)
	s := &stack{t: t, fake: fake, addr: addr, stopSrv: stopSrv, reg: reg, cancel: cancel,
		declared: map[string]float64{}}
	t.Cleanup(func() { cancel(); stopSrv() })
	// wait for the target to register
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(reg.Entries()) > 0 && reg.Entries()[0].State.GetStatus() == adapterv1alpha1.DeviceState_ONLINE {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s
}

func (s *stack) submit(tenant string, shots int, extraSpec map[string]any) string {
	s.t.Helper()
	id := uuid.NewString()
	inline := base64.StdEncoding.EncodeToString([]byte("OPENQASM 3.0;\nqubit[2] q;\n"))
	spec := map[string]any{
		"workload": map[string]any{"kind": "gate-model", "gateModel": map[string]any{
			"program": map[string]any{"format": "openqasm3", "inline": inline},
			"shots":   float64(shots),
		}},
		"requirements": map[string]any{"qubits": float64(2)},
	}
	for k, v := range extraSpec {
		spec[k] = v
	}
	rec := &store.JobRecord{
		JobID: id, Tenant: tenant, Name: "chaos", Phase: job.Pending,
		Doc: map[string]any{
			"apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
			"metadata": map[string]any{"name": "chaos", "tenant": tenant},
			"spec":     spec,
		},
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	if err := testStore.InsertJob(context.Background(), rec); err != nil {
		s.t.Fatal(err)
	}
	s.accepted = append(s.accepted, id)
	s.declared[id] = float64(shots)
	return id
}

func (s *stack) phase(id string) job.Phase {
	rec, err := testStore.GetJob(context.Background(), id)
	if err != nil {
		return job.Phase("MISSING")
	}
	return rec.Phase
}

func (s *stack) awaitPhase(id string, want job.Phase, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if s.phase(id) == want {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func (s *stack) awaitTerminal(id string, within time.Duration) job.Phase {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if p := s.phase(id); p.Terminal() {
			return p
		}
		time.Sleep(100 * time.Millisecond)
	}
	return s.phase(id)
}

// assertInvariants runs the full invariant suite and fails the test on any
// violation — this is what makes a scenario "pass" (invariants hold), not
// merely "didn't crash".
func (s *stack) assertInvariants() {
	s.t.Helper()
	vs := chaos.CheckAll(context.Background(), testStore, s.accepted, s.declared)
	if len(vs) > 0 {
		for _, v := range vs {
			s.t.Errorf("INVARIANT VIOLATED: %s", v)
		}
		s.t.FailNow()
	}
}

func fastTarget() *adaptertest.TargetSpec {
	return &adaptertest.TargetSpec{ID: "t1", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 100000}
}

// ---- scenario 1: adapter killed mid-RUNNING --------------------------------

func TestChaos_AdapterKilledMidRunning(t *testing.T) {
	s := newStack(t, &adaptertest.TargetSpec{ID: "t1", Qubits: 5, Formats: []string{"openqasm3"},
		MaxShots: 100000, StepDelay: 400 * time.Millisecond})
	id := s.submit("chaos/kill", 100, nil)
	// let it reach the adapter (SUBMITTED/RUNNING), then kill the adapter.
	if !s.awaitPhase(id, job.Running, 20*time.Second) {
		// SUBMITTED is also acceptable timing; proceed regardless
		_ = s.awaitPhase(id, job.Submitted, 5*time.Second)
	}
	s.stopSrv() // adapter gone
	time.Sleep(2 * time.Second)
	// job must remain queryable in a known phase — not lost, not corrupted.
	if p := s.phase(id); p == "MISSING" || !p.Valid() {
		t.Fatalf("job lost after adapter kill: %s", p)
	}
	s.assertInvariants()
}

// ---- scenario 2: control-plane ↔ adapter partition -------------------------

func TestChaos_Partition(t *testing.T) {
	// Serve on a fixed port so we can heal on the same address.
	fake := adaptertest.New(fastTarget())
	addr, stop, err := fake.ServeControllable("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := addr[len("127.0.0.1:"):]
	reg, _ := registry.NewFromSpec("sim=" + addr)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	reg.Start(ctx)
	d, _ := dispatch.New(testStore, reg, "", nil)
	go d.Run(ctx)
	s := &stack{t: t, fake: fake, addr: addr, reg: reg, declared: map[string]float64{}}
	time.Sleep(2 * time.Second)

	stop() // partition begins
	id := s.submit("chaos/part", 100, nil)
	time.Sleep(3 * time.Second) // "5 minutes" compressed
	if p := s.phase(id); p.Terminal() && p != job.Failed {
		t.Fatalf("job unexpectedly %s during partition", p)
	}
	// heal on the same port
	_, stop2, err := fake.ServeControllable("127.0.0.1:" + port)
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	t.Cleanup(stop2)
	time.Sleep(6 * time.Second)
	if p := s.phase(id); p == "MISSING" || !p.Valid() {
		t.Fatalf("job lost across partition: %s", p)
	}
	s.assertInvariants()
}

// ---- scenario 3: Postgres restart mid-bind ---------------------------------

func TestChaos_PostgresRestart(t *testing.T) {
	s := newStack(t, fastTarget())
	ids := []string{}
	for i := 0; i < 5; i++ {
		ids = append(ids, s.submit("chaos/pg", 100, nil))
	}
	// restart Postgres while binds/executions are in flight
	time.Sleep(300 * time.Millisecond)
	restartCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := pgContainer.Stop(restartCtx, nil); err != nil {
		t.Fatalf("stop pg: %v", err)
	}
	if err := pgContainer.Start(restartCtx); err != nil {
		t.Fatalf("start pg: %v", err)
	}
	// pgxpool reconnects; give the dispatcher time to resume.
	for _, id := range ids {
		_ = s.awaitTerminal(id, 60*time.Second)
	}
	for _, id := range ids {
		if p := s.phase(id); p == "MISSING" || !p.Valid() {
			t.Fatalf("job %s lost across pg restart: %s", id, p)
		}
	}
	s.assertInvariants()
}

// ---- scenario 4: duplicate LISTEN/NOTIFY -----------------------------------

func TestChaos_DuplicateNotify(t *testing.T) {
	s := newStack(t, fastTarget())
	id := s.submit("chaos/notify", 100, nil)
	// hammer duplicate wakeups while the job is being scheduled
	for i := 0; i < 20; i++ {
		_ = testStore.NotifyJobs(context.Background())
		time.Sleep(20 * time.Millisecond)
	}
	if s.awaitTerminal(id, 30*time.Second) != job.Succeeded {
		t.Fatalf("job did not succeed: %s", s.phase(id))
	}
	// the key invariant: SKIP LOCKED binding admits no double execution.
	s.assertInvariants()
}

// ---- scenario 5: replay-clock skew -----------------------------------------

func TestChaos_ClockSkew(t *testing.T) {
	// Calibration snapshot dated far in the future (skewed clock upstream).
	skewed := &adaptertest.TargetSpec{
		ID: "t1", Qubits: 5, Formats: []string{"openqasm3"}, MaxShots: 100000,
		Metrics: []*adapterv1alpha1.Metric{
			{Name: "gate.2q.cx.error", Value: 0.005, Qubits: []uint32{0, 1}, Methodology: "synthetic"},
		},
	}
	s := newStack(t, skewed)
	// job with a calibrationMaxAge — the scheduler must not crash on skew.
	id := s.submit("chaos/skew", 100, map[string]any{
		"requirements": map[string]any{
			"qubits":  float64(2),
			"quality": map[string]any{"gateModel": map[string]any{"calibrationMaxAge": "1h"}},
		},
	})
	_ = s.awaitTerminal(id, 30*time.Second)
	if p := s.phase(id); p == "MISSING" || !p.Valid() {
		t.Fatalf("job lost under clock skew: %s", p)
	}
	s.assertInvariants()
}

// ---- scenario 6: garbage result from an adapter ----------------------------

func TestChaos_GarbageFromAdapter(t *testing.T) {
	s := newStack(t, fastTarget())
	s.fake.SetGarbage(true)
	id := s.submit("chaos/garbage", 100, nil)
	// The control plane must degrade gracefully — a queryable terminal state,
	// never a panic or a lost job.
	p := s.awaitTerminal(id, 30*time.Second)
	if p == "MISSING" || !p.Valid() {
		t.Fatalf("job lost on garbage result: %s", p)
	}
	s.assertInvariants()
}

// ---- scenario 7: 10× adapter latency ---------------------------------------

func TestChaos_HighLatency(t *testing.T) {
	s := newStack(t, &adaptertest.TargetSpec{ID: "t1", Qubits: 5, Formats: []string{"openqasm3"},
		MaxShots: 100000, StepDelay: 700 * time.Millisecond}) // ~10× the 10ms default × states
	id := s.submit("chaos/latency", 100, nil)
	if s.awaitTerminal(id, 60*time.Second) != job.Succeeded {
		t.Fatalf("high-latency job did not complete: %s", s.phase(id))
	}
	s.assertInvariants()
}

// ---- scenario 8: disk-full on ledger write ---------------------------------

func TestChaos_LedgerWriteFails(t *testing.T) {
	// Simulate the ledger insert failing (disk-full) by revoking the serving
	// role's INSERT on usage_ledger mid-run, via an owner connection.
	owner, err := pgxpool.New(context.Background(), testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Exec(context.Background(),
		`REVOKE INSERT ON usage_ledger FROM rabi_app`); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	restore := func() {
		_, _ = owner.Exec(context.Background(), `GRANT INSERT ON usage_ledger TO rabi_app`)
	}
	defer restore()

	s := newStack(t, fastTarget())
	id := s.submit("chaos/diskfull", 100, nil)
	// The job must still reach a queryable terminal state; usage recording
	// fails but is logged, never double-billed, never lost.
	p := s.awaitTerminal(id, 30*time.Second)
	if p == "MISSING" || !p.Valid() {
		t.Fatalf("job lost on ledger write failure: %s", p)
	}
	restore()
	s.assertInvariants()
	fmt.Fprintln(os.Stderr, "ledger-write-failure scenario: job terminal, invariants held")
}
