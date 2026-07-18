// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

const (
	defaultPageSize = 100
	maxPageSize     = 1000
	watchPollEvery  = 200 * time.Millisecond
)

// FleetViewer provides the admission validator's view of the fleet.
type FleetViewer interface {
	FleetView(ctx context.Context) job.FleetView
}

// TaskCanceller propagates job cancellation to in-flight adapter tasks.
type TaskCanceller interface {
	CancelJob(ctx context.Context, jobID string) error
}

// jobsService implements tangle.api.v1alpha1.JobsService backed by Postgres.
type jobsService struct {
	apiv1alpha1.UnimplementedJobsServiceServer
	store     *store.Store
	validator *job.Validator
	fleet     FleetViewer
	canceller TaskCanceller
}

func (s *jobsService) SubmitJob(ctx context.Context, req *apiv1alpha1.SubmitJobRequest) (*apiv1alpha1.Job, error) {
	if req.GetQuantumJob() == nil {
		return nil, status.Error(codes.InvalidArgument, "quantum_job document is required")
	}
	adm, err := s.validator.Admit(req.GetQuantumJob().AsMap(), req.GetTenant(), s.fleet.FleetView(ctx))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "admission: %v", err)
	}
	if err := auth.CheckProject(ctx, adm.Tenant); err != nil {
		return nil, err
	}

	conditions := make([]any, 0, len(adm.Warnings)+1)
	for _, w := range adm.Warnings {
		conditions = append(conditions, conditionToMap(w))
	}

	if req.GetDryRun() {
		// Validation-only: nothing is written (docs/decisions.md D-011).
		conditions = append(conditions, conditionToMap(job.Condition{
			Type: "Validated", Status: "True", Reason: "DryRun",
			Message: "admission checks passed; job was not enqueued",
		}))
		jobStatus := map[string]any{"conditions": conditions}
		return jobToProto(&store.JobRecord{
			Tenant: adm.Tenant,
			Name:   adm.Name,
			Doc:    adm.Doc,
			Status: jobStatus,
		})
	}

	rec := &store.JobRecord{
		JobID:  uuid.NewString(),
		Tenant: adm.Tenant,
		Name:   adm.Name,
		Phase:  job.Pending,
		Doc:    adm.Doc,
		Status: map[string]any{
			"phase":      string(job.Pending),
			"conditions": conditions,
			"tasks":      []any{},
			"usage":      []any{},
		},
	}
	if err := s.store.InsertJob(ctx, rec); err != nil {
		return nil, status.Errorf(codes.Internal, "persisting job: %v", err)
	}
	return jobToProto(rec)
}

func (s *jobsService) GetJob(ctx context.Context, ref *apiv1alpha1.JobRef) (*apiv1alpha1.Job, error) {
	rec, err := s.loadJob(ctx, ref.GetJobId())
	if err != nil {
		return nil, err
	}
	return jobToProto(rec)
}

func (s *jobsService) ListJobs(ctx context.Context, req *apiv1alpha1.ListJobsRequest) (*apiv1alpha1.ListJobsResponse, error) {
	pageSize := int(req.GetPageSize())
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset := 0
	if tok := req.GetPageToken(); tok != "" {
		var err error
		if offset, err = strconv.Atoi(tok); err != nil || offset < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "malformed page_token %q", tok)
		}
	}
	tenant, err := effectiveTenant(ctx, req.GetTenant())
	if err != nil {
		return nil, err
	}
	recs, err := s.store.ListJobs(ctx, tenant, req.GetPhaseFilter(), pageSize, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "listing jobs: %v", err)
	}
	resp := &apiv1alpha1.ListJobsResponse{}
	for _, rec := range recs {
		j, err := jobToProto(rec)
		if err != nil {
			return nil, err
		}
		resp.Jobs = append(resp.Jobs, j)
	}
	if len(recs) == pageSize {
		resp.NextPageToken = strconv.Itoa(offset + pageSize)
	}
	return resp, nil
}

// WatchJob streams the job once immediately, then every transition in event
// order until the job is terminal or the client goes away. Events are read
// from the append-only job_events table, so no transition can be skipped.
func (s *jobsService) WatchJob(ref *apiv1alpha1.JobRef, stream apiv1alpha1.JobsService_WatchJobServer) error {
	ctx := stream.Context()
	rec, err := s.loadJob(ctx, ref.GetJobId())
	if err != nil {
		return err
	}

	events, err := s.store.JobEventsSince(ctx, rec.JobID, 0)
	if err != nil {
		return status.Errorf(codes.Internal, "reading job events: %v", err)
	}
	var lastSeq int64
	sent := false
	for _, ev := range events {
		if err := s.sendEvent(stream, rec, ev); err != nil {
			return err
		}
		lastSeq = ev.Seq
		sent = true
		if ev.Phase.Terminal() {
			return nil
		}
	}
	if !sent { // no events would be a bug, but never leave the client blind
		if err := stream.Send(mustJobProto(rec)); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(watchPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-ticker.C:
		}
		events, err := s.store.JobEventsSince(ctx, rec.JobID, lastSeq)
		if err != nil {
			return status.Errorf(codes.Internal, "reading job events: %v", err)
		}
		for _, ev := range events {
			if err := s.sendEvent(stream, rec, ev); err != nil {
				return err
			}
			lastSeq = ev.Seq
			if ev.Phase.Terminal() {
				return nil
			}
		}
	}
}

func (s *jobsService) sendEvent(stream apiv1alpha1.JobsService_WatchJobServer, rec *store.JobRecord, ev *store.JobEvent) error {
	view := *rec
	view.Phase = ev.Phase
	view.Status = ev.Status
	view.UpdatedAt = ev.CreatedAt
	return stream.Send(mustJobProto(&view))
}

func (s *jobsService) CancelJob(ctx context.Context, ref *apiv1alpha1.JobRef) (*apiv1alpha1.Job, error) {
	// Load first so a project-scoped token cannot cancel outside its project.
	if _, err := s.loadJob(ctx, ref.GetJobId()); err != nil {
		return nil, err
	}
	if s.canceller != nil {
		if err := s.canceller.CancelJob(ctx, ref.GetJobId()); err != nil {
			return nil, status.Errorf(codes.Internal, "cancelling task: %v", err)
		}
	}
	rec, err := s.store.TransitionJob(ctx, ref.GetJobId(), job.Cancelled, func(st map[string]any) map[string]any {
		return appendCondition(st, job.Condition{
			Type: "Cancelled", Status: "True", Reason: "UserRequested",
			Message: "cancelled via CancelJob",
		})
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "job %q not found", ref.GetJobId())
	}
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cancel: %v", err)
	}
	return jobToProto(rec)
}

func (s *jobsService) loadJob(ctx context.Context, jobID string) (*store.JobRecord, error) {
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}
	rec, err := s.store.GetJob(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "job %q not found", jobID)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "loading job: %v", err)
	}
	if err := auth.CheckProject(ctx, rec.Tenant); err != nil {
		return nil, err
	}
	return rec, nil
}

// effectiveTenant resolves a requested tenant filter against the caller's
// project scope: scoped tokens default to (and may not leave) their project.
func effectiveTenant(ctx context.Context, requested string) (string, error) {
	p, ok := auth.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no principal in context")
	}
	if p.Project == "" {
		return requested, nil
	}
	if requested == "" {
		return p.Project, nil
	}
	return requested, auth.CheckProject(ctx, requested)
}

func appendCondition(st map[string]any, c job.Condition) map[string]any {
	if st == nil {
		st = map[string]any{}
	}
	conditions, _ := st["conditions"].([]any)
	st["conditions"] = append(conditions, conditionToMap(c))
	return st
}

func conditionToMap(c job.Condition) map[string]any {
	raw, _ := json.Marshal(c)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func jobToProto(rec *store.JobRecord) (*apiv1alpha1.Job, error) {
	doc, err := structpb.NewStruct(rec.Doc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encoding job document: %v", err)
	}
	st, err := structpb.NewStruct(rec.Status)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encoding job status: %v", err)
	}
	j := &apiv1alpha1.Job{
		JobId:      rec.JobID,
		Tenant:     rec.Tenant,
		QuantumJob: doc,
		Status:     st,
	}
	if !rec.CreatedAt.IsZero() {
		j.CreatedAt = timestamppb.New(rec.CreatedAt)
	}
	if !rec.UpdatedAt.IsZero() {
		j.UpdatedAt = timestamppb.New(rec.UpdatedAt)
	}
	return j, nil
}

func mustJobProto(rec *store.JobRecord) *apiv1alpha1.Job {
	j, err := jobToProto(rec)
	if err != nil {
		// Status documents are produced by this process from JSON; a
		// non-encodable one is a programming error.
		panic(fmt.Sprintf("unencodable job record: %v", err))
	}
	return j
}
