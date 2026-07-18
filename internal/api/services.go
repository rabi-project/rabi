// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

// TargetLister is the registry surface the API needs.
type TargetLister interface {
	ListTargets(ctx context.Context, modalityFilter string) ([]*apiv1alpha1.Target, error)
	GetTarget(ctx context.Context, name string) (*apiv1alpha1.Target, error)
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

// usageService serves the native-unit usage ledger. Native units only —
// normalization/pricing is an explicit MVP non-goal.
type usageService struct {
	apiv1alpha1.UnimplementedUsageServiceServer
	store *store.Store
}

func (s *usageService) GetTenantUsage(ctx context.Context, req *apiv1alpha1.TenantUsageRequest) (*apiv1alpha1.TenantUsageResponse, error) {
	if req.GetTenant() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant is required")
	}
	if err := auth.CheckProject(ctx, req.GetTenant()); err != nil {
		return nil, err
	}
	var from, to time.Time
	if req.GetFrom() != nil {
		from = req.GetFrom().AsTime()
	}
	if req.GetTo() != nil {
		to = req.GetTo().AsTime()
	}
	totals, err := s.store.TenantUsage(ctx, req.GetTenant(), from, to)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "aggregating usage: %v", err)
	}
	resp := &apiv1alpha1.TenantUsageResponse{}
	for _, u := range totals {
		resp.Usage = append(resp.Usage, &apiv1alpha1.NativeUsage{
			Target: u.Target, Unit: u.Unit, Amount: u.Amount,
		})
	}
	return resp, nil
}
