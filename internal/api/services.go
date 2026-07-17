// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
)

// TargetLister is the registry surface the API needs.
type TargetLister interface {
	ListTargets(ctx context.Context, modalityFilter string) ([]*apiv1alpha1.Target, error)
	GetTarget(ctx context.Context, name string) (*apiv1alpha1.Target, error)
}

// jobsService is the client-facing JobsService. Job persistence and the
// lifecycle state machine arrive with M1; until then every method is a
// well-formed Unimplemented.
type jobsService struct {
	apiv1alpha1.UnimplementedJobsServiceServer
}

func (s *jobsService) SubmitJob(context.Context, *apiv1alpha1.SubmitJobRequest) (*apiv1alpha1.Job, error) {
	return nil, status.Error(codes.Unimplemented, "SubmitJob: job store lands in M1")
}

func (s *jobsService) GetJob(context.Context, *apiv1alpha1.JobRef) (*apiv1alpha1.Job, error) {
	return nil, status.Error(codes.Unimplemented, "GetJob: job store lands in M1")
}

func (s *jobsService) ListJobs(context.Context, *apiv1alpha1.ListJobsRequest) (*apiv1alpha1.ListJobsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListJobs: job store lands in M1")
}

func (s *jobsService) WatchJob(*apiv1alpha1.JobRef, apiv1alpha1.JobsService_WatchJobServer) error {
	return status.Error(codes.Unimplemented, "WatchJob: job store lands in M1")
}

func (s *jobsService) CancelJob(context.Context, *apiv1alpha1.JobRef) (*apiv1alpha1.Job, error) {
	return nil, status.Error(codes.Unimplemented, "CancelJob: job store lands in M1")
}

// targetsService serves the fleet view from the registry.
type targetsService struct {
	apiv1alpha1.UnimplementedTargetsServiceServer
	registry TargetLister
}

func (s *targetsService) ListTargets(ctx context.Context, req *apiv1alpha1.ListTargetsRequest) (*apiv1alpha1.ListTargetsResponse, error) {
	targets, err := s.registry.ListTargets(ctx, req.GetModalityFilter())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "listing targets: %v", err)
	}
	return &apiv1alpha1.ListTargetsResponse{Targets: targets}, nil
}

func (s *targetsService) GetTarget(ctx context.Context, req *apiv1alpha1.TargetRef) (*apiv1alpha1.Target, error) {
	target, err := s.registry.GetTarget(ctx, req.GetName())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "getting target: %v", err)
	}
	if target == nil {
		return nil, status.Errorf(codes.NotFound, "target %q not registered", req.GetName())
	}
	return target, nil
}

// usageService serves the native-unit usage ledger; accounting lands in M2.
type usageService struct {
	apiv1alpha1.UnimplementedUsageServiceServer
}

func (s *usageService) GetTenantUsage(context.Context, *apiv1alpha1.TenantUsageRequest) (*apiv1alpha1.TenantUsageResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetTenantUsage: accounting lands in M2")
}
