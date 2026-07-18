// SPDX-License-Identifier: Apache-2.0

// Project-scope enforcement for API tokens: a token bound to project X can
// act only inside X, whatever its role (roles gate verbs; scope gates nouns).
package api_test

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

func mintProjectToken(t *testing.T, project, role string) string {
	t.Helper()
	plain, id, hash, err := auth.MintToken()
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.InsertToken(t.Context(), &store.TokenRecord{
		ID: id, Name: "scope-" + project, Project: project, Role: role,
		TokenHash: hash, CreatedBy: "scope_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestTokenProjectScope(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	jobs := apiv1alpha1.NewJobsServiceClient(conn)
	usage := apiv1alpha1.NewUsageServiceClient(conn)

	insider := mintProjectToken(t, "acme/qa", "member")
	outsider := mintProjectToken(t, "rival/lab", "member")

	withTok := func(tok string) metadata.MD {
		return metadata.Pairs("authorization", "Bearer "+tok)
	}
	doc := bellJob(t, "scope-check")

	// The insider submits into its own project.
	inCtx := metadata.NewOutgoingContext(t.Context(), withTok(insider))
	created, err := jobs.SubmitJob(inCtx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc, Tenant: "acme/qa"})
	if err != nil {
		t.Fatalf("in-project submit must work: %v", err)
	}

	// The outsider cannot submit into acme/qa...
	outCtx := metadata.NewOutgoingContext(t.Context(), withTok(outsider))
	_, err = jobs.SubmitJob(outCtx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc, Tenant: "acme/qa"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-project submit must be PermissionDenied, got %v", err)
	}
	// ...nor read, cancel, or meter the insider's job/project.
	if _, err = jobs.GetJob(outCtx, &apiv1alpha1.JobRef{JobId: created.GetJobId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-project get must be PermissionDenied, got %v", err)
	}
	if _, err = jobs.CancelJob(outCtx, &apiv1alpha1.JobRef{JobId: created.GetJobId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-project cancel must be PermissionDenied, got %v", err)
	}
	if _, err = usage.GetTenantUsage(outCtx, &apiv1alpha1.TenantUsageRequest{Tenant: "acme/qa"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-project usage must be PermissionDenied, got %v", err)
	}

	// A scoped token listing with no filter sees only its own project.
	list, err := jobs.ListJobs(outCtx, &apiv1alpha1.ListJobsRequest{})
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	for _, j := range list.GetJobs() {
		if j.GetTenant() != "rival/lab" {
			t.Fatalf("scoped list leaked tenant %q", j.GetTenant())
		}
	}
}
