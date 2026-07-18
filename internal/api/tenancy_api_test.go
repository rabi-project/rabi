// SPDX-License-Identifier: Apache-2.0

// M2 over the wire: quotas reject with ResourceExhausted, archived projects
// reject with FailedPrecondition, and the admin project/quota lifecycle
// round-trips.
package api_test

import (
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
)

func adminClient(t *testing.T) (adminv1alpha1.AdminServiceClient, apiv1alpha1.JobsServiceClient, func() *metadata.MD) {
	t.Helper()
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return adminv1alpha1.NewAdminServiceClient(conn), apiv1alpha1.NewJobsServiceClient(conn), nil
}

func TestQuotaOverTheWire(t *testing.T) {
	admin, jobs, _ := adminClient(t)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)

	// bellJob fixtures submit under acme/qa with 1000 shots each.
	const tenant = "acme/qa"
	if _, err := admin.CreateProject(ctx, &adminv1alpha1.CreateProjectRequest{Tenant: tenant}); err != nil {
		t.Fatal(err)
	}
	// Room for one more 1000-shot job on top of whatever this suite already
	// committed for acme/qa: read current committed via a probe submission.
	quotas, err := admin.ListQuotas(ctx, &adminv1alpha1.ListQuotasRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_ = quotas

	first, err := jobs.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: bellJob(t, "quota-wire-1"), Tenant: tenant})
	if err != nil {
		t.Fatalf("baseline submit: %v", err)
	}
	_ = first

	// Freeze the quota at exactly the currently committed amount: the next
	// submission must bounce with ResourceExhausted.
	var committed float64
	if err := testStore.Pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(amount) FROM usage_ledger WHERE tenant = $1 AND unit = 'shots'), 0)
		     + COALESCE((SELECT SUM(declared_cost(doc, 'shots')) FROM jobs
		                 WHERE tenant = $1 AND phase NOT IN ('SUCCEEDED','FAILED','CANCELLED')), 0)`,
		tenant).Scan(&committed); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.SetQuota(ctx, &adminv1alpha1.SetQuotaRequest{Tenant: tenant, Unit: "shots", Limit: committed}); err != nil {
		t.Fatal(err)
	}
	_, err = jobs.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: bellJob(t, "quota-wire-2"), Tenant: tenant})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("over-quota submit: want ResourceExhausted, got %v", err)
	}
	// Lift the quota so later suites are unaffected.
	if _, err := admin.SetQuota(ctx, &adminv1alpha1.SetQuotaRequest{Tenant: tenant, Unit: "shots", Limit: -1}); err != nil {
		t.Fatal(err)
	}
}

func TestArchivedProjectRejectsSubmissions(t *testing.T) {
	admin, jobs, _ := adminClient(t)
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)

	// Archiving is one-way, so use a dedicated project (bellJob's doc pins
	// tenant acme/qa — rewrite its envelope).
	doc := bellJob(t, "archived-probe")
	doc.GetFields()["metadata"].GetStructValue().Fields["tenant"] = structpb.NewStringValue("mothballed/lab")

	if _, err := admin.CreateProject(ctx, &adminv1alpha1.CreateProjectRequest{Tenant: "mothballed/lab"}); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc, Tenant: "mothballed/lab"}); err != nil {
		t.Fatalf("pre-archive submit must work: %v", err)
	}
	resp, err := admin.ArchiveProject(ctx, &adminv1alpha1.ArchiveProjectRequest{Tenant: "mothballed/lab"})
	if err != nil || !resp.GetFound() {
		t.Fatalf("archive: %v found=%v", err, resp.GetFound())
	}
	_, err = jobs.SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{QuantumJob: doc, Tenant: "mothballed/lab"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("archived submit: want FailedPrecondition, got %v", err)
	}
}
