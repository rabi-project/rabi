// SPDX-License-Identifier: Apache-2.0

// T1.api component suite: the real API server against a real Postgres
// (testcontainers), exercised over real gRPC.
package api_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/yaml"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

const testAPIKey = "component-test-key"

var (
	testDSN   string
	testStore *store.Store
	testAddr  string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"),
		tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("starting postgres container: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()

	testDSN, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatalf("postgres dsn: %v", err)
	}
	testStore, err = store.Open(ctx, testDSN)
	if err != nil {
		log.Fatalf("opening store: %v", err)
	}
	defer testStore.Close()

	srv, err := newServer(testStore)
	if err != nil {
		log.Fatalf("assembling server: %v", err)
	}
	testAddr = srv.GRPCAddr()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		if err := srv.Run(runCtx); err != nil {
			log.Printf("server exited: %v", err)
		}
	}()

	os.Exit(m.Run())
}

func newServer(st *store.Store) (*api.Server, error) {
	validator, err := job.NewValidator()
	if err != nil {
		return nil, err
	}
	// The component suite authenticates with the bootstrap token (an admin
	// principal); role-specific behavior is covered by the authz matrix suite.
	authn, err := api.NewAuthenticator(testAPIKey, nil, st, slog.Default())
	if err != nil {
		return nil, err
	}
	reg := registry.New()
	return api.New(api.Config{
		GRPCAddr:  "127.0.0.1:0",
		HTTPAddr:  "127.0.0.1:0",
		Auth:      authn,
		Registry:  reg,
		Fleet:     reg,
		Store:     st,
		Validator: validator,
	})
}

func client(t *testing.T) (apiv1alpha1.JobsServiceClient, context.Context) {
	t.Helper()
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)
	return apiv1alpha1.NewJobsServiceClient(conn), ctx
}

func bellJob(t *testing.T, name string) *structpb.Struct {
	t.Helper()
	docYAML := fmt.Sprintf(`
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: %s, tenant: acme/qa }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00gMy4wOw== }
      shots: 1000
`, name)
	asJSON, err := yaml.YAMLToJSON([]byte(docYAML))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	doc := &structpb.Struct{}
	if err := protojson.Unmarshal(asJSON, doc); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return doc
}

func phaseOf(j *apiv1alpha1.Job) string {
	p, _ := j.GetStatus().AsMap()["phase"].(string)
	return p
}

// Submit→Get round trip, including the p95 < 200 ms local latency bar.
func TestSubmitGetRoundTrip(t *testing.T) {
	c, ctx := client(t)
	const samples = 20
	latencies := make([]time.Duration, 0, samples)
	for i := range samples {
		start := time.Now()
		submitted, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{
			QuantumJob: bellJob(t, fmt.Sprintf("rt-%d", i)),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		got, err := c.GetJob(ctx, &apiv1alpha1.JobRef{JobId: submitted.GetJobId()})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		latencies = append(latencies, time.Since(start))

		if got.GetTenant() != "acme/qa" {
			t.Fatalf("tenant = %q, want acme/qa", got.GetTenant())
		}
		if phaseOf(got) != "PENDING" {
			t.Fatalf("phase = %q, want PENDING", phaseOf(got))
		}
		if got.GetCreatedAt() == nil || got.GetUpdatedAt() == nil {
			t.Fatal("timestamps missing")
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[int(float64(samples)*0.95)-1]
	if p95 > 200*time.Millisecond {
		t.Fatalf("submit→get p95 = %v, bar is 200ms", p95)
	}
}

// dry_run must write nothing — asserted by row counts (T1.api).
func TestDryRunWritesNothing(t *testing.T) {
	c, ctx := client(t)
	jobsBefore, err := testStore.CountRows(ctx, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := testStore.CountRows(ctx, "job_events")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{
		QuantumJob: bellJob(t, "dry-run-probe"),
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("dry-run submit: %v", err)
	}
	if resp.GetJobId() != "" {
		t.Fatalf("dry run returned a job id %q; nothing may be enqueued", resp.GetJobId())
	}

	jobsAfter, _ := testStore.CountRows(ctx, "jobs")
	eventsAfter, _ := testStore.CountRows(ctx, "job_events")
	if jobsAfter != jobsBefore || eventsAfter != eventsBefore {
		t.Fatalf("dry run wrote rows: jobs %d→%d, events %d→%d", jobsBefore, jobsAfter, eventsBefore, eventsAfter)
	}
}

// The watch stream must deliver every transition, in order (T1.api).
func TestWatchDeliversTransitionsInOrder(t *testing.T) {
	c, ctx := client(t)
	submitted, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: bellJob(t, "watched")})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	id := submitted.GetJobId()

	stream, err := c.WatchJob(ctx, &apiv1alpha1.JobRef{JobId: id})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Drive the full lifecycle through the store (the scheduler's path).
	go func() {
		for _, next := range []job.Phase{job.Scheduled, job.Submitted, job.Running, job.Succeeded} {
			time.Sleep(50 * time.Millisecond)
			if _, err := testStore.TransitionJob(context.Background(), id, next, nil); err != nil {
				t.Errorf("transition to %s: %v", next, err)
				return
			}
		}
	}()

	var phases []string
	for {
		j, err := stream.Recv()
		if err != nil {
			break
		}
		phases = append(phases, phaseOf(j))
		if job.Phase(phases[len(phases)-1]).Terminal() {
			break
		}
	}
	want := []string{"PENDING", "SCHEDULED", "SUBMITTED", "RUNNING", "SUCCEEDED"}
	if len(phases) != len(want) {
		t.Fatalf("phases = %v, want %v", phases, want)
	}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phases = %v, want %v", phases, want)
		}
	}
}

// PENDING → CANCELLED via the API; terminal jobs refuse further cancels.
func TestCancelLifecycle(t *testing.T) {
	c, ctx := client(t)
	submitted, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: bellJob(t, "cancel-me")})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	cancelled, err := c.CancelJob(ctx, &apiv1alpha1.JobRef{JobId: submitted.GetJobId()})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if phaseOf(cancelled) != "CANCELLED" {
		t.Fatalf("phase = %q, want CANCELLED", phaseOf(cancelled))
	}

	_, err = c.CancelJob(ctx, &apiv1alpha1.JobRef{JobId: submitted.GetJobId()})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("second cancel: code = %v, want FailedPrecondition (%v)", status.Code(err), err)
	}

	_, err = c.CancelJob(ctx, &apiv1alpha1.JobRef{JobId: "00000000-0000-0000-0000-000000000000"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("cancel unknown job: code = %v, want NotFound", status.Code(err))
	}
}

// Admission failures surface as precise InvalidArgument errors.
func TestSubmitRejectsInvalid(t *testing.T) {
	c, ctx := client(t)
	doc := bellJob(t, "bad-kind")
	doc.Fields["spec"].GetStructValue().Fields["workload"].GetStructValue().Fields["kind"] =
		structpb.NewStringValue("photonic")
	_, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
	}
	if !strings.Contains(err.Error(), "/spec/workload/kind") {
		t.Fatalf("error not precise about the field: %v", err)
	}
}

// A restart of rabi loses nothing: a second server over the same Postgres
// sees every job (T1.api / M1 acceptance).
func TestRestartLosesNothing(t *testing.T) {
	c, ctx := client(t)
	ids := map[string]bool{}
	for i := range 5 {
		submitted, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{
			QuantumJob: bellJob(t, fmt.Sprintf("restart-%d", i)),
		})
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
		ids[submitted.GetJobId()] = true
	}

	// "Restart": a fresh store connection and a fresh API server instance.
	st2, err := store.Open(ctx, testDSN)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()
	srv2, err := newServer(st2)
	if err != nil {
		t.Fatalf("second server: %v", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = srv2.Run(runCtx) }()

	conn2, err := grpc.NewClient(srv2.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial second server: %v", err)
	}
	defer func() { _ = conn2.Close() }()
	ctx2 := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)
	c2 := apiv1alpha1.NewJobsServiceClient(conn2)

	for id := range ids {
		got, err := c2.GetJob(ctx2, &apiv1alpha1.JobRef{JobId: id})
		if err != nil {
			t.Fatalf("job %s lost across restart: %v", id, err)
		}
		if phaseOf(got) != "PENDING" {
			t.Fatalf("job %s phase = %q, want PENDING", id, phaseOf(got))
		}
	}
}

func TestListJobsFilters(t *testing.T) {
	c, ctx := client(t)
	if _, err := c.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: bellJob(t, "list-probe")}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	resp, err := c.ListJobs(ctx, &apiv1alpha1.ListJobsRequest{Tenant: "acme/qa", PhaseFilter: "PENDING"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.GetJobs()) == 0 {
		t.Fatal("expected at least one PENDING job for acme/qa")
	}
	for _, j := range resp.GetJobs() {
		if j.GetTenant() != "acme/qa" || phaseOf(j) != "PENDING" {
			t.Fatalf("filter leak: tenant=%s phase=%s", j.GetTenant(), phaseOf(j))
		}
	}

	none, err := c.ListJobs(ctx, &apiv1alpha1.ListJobsRequest{Tenant: "nobody"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(none.GetJobs()) != 0 {
		t.Fatalf("expected zero jobs for unknown tenant, got %d", len(none.GetJobs()))
	}
}
