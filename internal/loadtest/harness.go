// SPDX-License-Identifier: Apache-2.0

package loadtest

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/adaptertest"
	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

// harnessToken is the bootstrap (admin) token the harness authenticates with;
// role behavior is out of scope for a load test.
const harnessToken = "loadtest-bootstrap-token"

// Stack is the full control-plane stack booted in-process for a load or soak
// run: a synthetic fleet (one fake adapter presenting many targets), the real
// registry, the real dispatcher, and the real API server — the same code paths
// production runs, against a caller-provided Postgres.
type Stack struct {
	Store      *store.Store
	Dispatcher *dispatch.Dispatcher
	Server     *api.Server
	GRPCAddr   string
	Targets    int

	fakeStop func()
	cancel   context.CancelFunc
}

// NewStack boots the stack against st with numTargets synthetic targets and
// waits for the fleet to come online. policy names the scheduling policy
// (empty = fifo/v0). Call Close when done.
func NewStack(ctx context.Context, st *store.Store, numTargets int, policy string) (*Stack, error) {
	if numTargets <= 0 {
		numTargets = 1
	}
	specs := make([]*adaptertest.TargetSpec, numTargets)
	for i := range specs {
		specs[i] = &adaptertest.TargetSpec{
			ID:       fmt.Sprintf("t%03d", i),
			Qubits:   32,
			Formats:  []string{"openqasm3"},
			MaxShots: 1_000_000,
			// Fast simulated execution: a load test measures the control plane,
			// not the backend, so tasks should clear quickly.
			StepDelay: time.Millisecond,
		}
	}
	fake := adaptertest.New(specs...)
	addr, fakeStop, err := fake.ServeControllable("127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("serve fake fleet: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	reg, err := registry.NewFromSpec("synth=" + addr)
	if err != nil {
		fakeStop()
		cancel()
		return nil, fmt.Errorf("registry: %w", err)
	}
	reg.Start(runCtx)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher, err := dispatch.New(st, reg, policy, logger)
	if err != nil {
		fakeStop()
		cancel()
		return nil, fmt.Errorf("dispatcher: %w", err)
	}
	go dispatcher.Run(runCtx)

	validator, err := job.NewValidator()
	if err != nil {
		fakeStop()
		cancel()
		return nil, fmt.Errorf("validator: %w", err)
	}
	authn, err := api.NewAuthenticator(harnessToken, nil, st, logger)
	if err != nil {
		fakeStop()
		cancel()
		return nil, fmt.Errorf("authenticator: %w", err)
	}
	srv, err := api.New(api.Config{
		GRPCAddr:  "127.0.0.1:0",
		HTTPAddr:  "127.0.0.1:0",
		Auth:      authn,
		Registry:  reg,
		Fleet:     reg,
		Store:     st,
		Validator: validator,
		Canceller: dispatcher,
	})
	if err != nil {
		fakeStop()
		cancel()
		return nil, fmt.Errorf("api server: %w", err)
	}
	go func() { _ = srv.Run(runCtx) }()

	s := &Stack{
		Store: st, Dispatcher: dispatcher, Server: srv,
		GRPCAddr: srv.GRPCAddr(), Targets: numTargets,
		fakeStop: fakeStop, cancel: cancel,
	}

	// Wait for the synthetic fleet to register (discovery is async).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		online := 0
		for _, e := range reg.Entries() {
			if e.State.GetStatus() == adapterv1alpha1.DeviceState_ONLINE {
				online++
			}
		}
		if online >= numTargets {
			return s, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Close()
	return nil, fmt.Errorf("only some of %d synthetic targets came online within 30s", numTargets)
}

// Close stops the stack.
func (s *Stack) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.fakeStop != nil {
		s.fakeStop()
	}
}

// Client returns a JobsService gRPC client and an auth'd context.
func (s *Stack) Client() (apiv1alpha1.JobsServiceClient, context.Context, func() error, error) {
	conn, err := grpc.NewClient(s.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, nil, err
	}
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+harnessToken)
	return apiv1alpha1.NewJobsServiceClient(conn), ctx, conn.Close, nil
}

// minimalDoc builds a valid gate-model QuantumJob document for tenant.
func minimalDoc(name, tenant string, shots int) map[string]any {
	inline := base64.StdEncoding.EncodeToString([]byte("OPENQASM 3.0;\nqubit[2] q;\n"))
	return map[string]any{
		"apiVersion": "tangle.dev/v1alpha1",
		"kind":       "QuantumJob",
		"metadata":   map[string]any{"name": name, "tenant": tenant},
		"spec": map[string]any{
			"workload": map[string]any{
				"kind": "gate-model",
				"gateModel": map[string]any{
					"program": map[string]any{"format": "openqasm3", "inline": inline},
					"shots":   float64(shots),
				},
			},
			"requirements": map[string]any{"qubits": float64(2)},
		},
	}
}

// SeedJob inserts one PENDING job directly into the store (the fast path for
// building a backlog, bypassing the API). Returns the job id.
func (s *Stack) SeedJob(ctx context.Context, tenant string, shots int) (string, error) {
	id := uuid.NewString()
	rec := &store.JobRecord{
		JobID: id, Tenant: tenant, Name: "load", Phase: job.Pending,
		Doc:    minimalDoc("load", tenant, shots),
		Status: map[string]any{"phase": "PENDING", "conditions": []any{}},
	}
	if err := s.Store.InsertJob(ctx, rec); err != nil {
		return "", err
	}
	return id, nil
}

// PendingCount returns the current number of PENDING jobs — the queue depth.
func (s *Stack) PendingCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE phase = 'PENDING'`).Scan(&n)
	return n, err
}

// NonTerminalOlderThan counts jobs that are still non-terminal and were created
// more than age ago — the soak "stuck job" check.
func (s *Stack) NonTerminalOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	var n int64
	cutoff := time.Now().Add(-age)
	err := s.Store.Pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs
		WHERE phase NOT IN ('SUCCEEDED','FAILED','CANCELLED') AND created_at < $1`, cutoff).Scan(&n)
	return n, err
}
