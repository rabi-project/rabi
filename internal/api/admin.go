// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

// adminService implements rabi.admin.v1alpha1.AdminService: token lifecycle
// and WhoAmI. Authorization happened in the interceptor; handlers only add
// semantic checks.
type adminService struct {
	adminv1alpha1.UnimplementedAdminServiceServer
	store *store.Store
}

func (a *adminService) WhoAmI(ctx context.Context, _ *adminv1alpha1.WhoAmIRequest) (*adminv1alpha1.WhoAmIResponse, error) {
	p, ok := auth.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no principal")
	}
	return &adminv1alpha1.WhoAmIResponse{
		Subject:       p.Subject,
		Name:          p.Name,
		Role:          p.Role.String(),
		PrincipalType: string(p.Type),
		Project:       p.Project,
	}, nil
}

func (a *adminService) CreateToken(ctx context.Context, req *adminv1alpha1.CreateTokenRequest) (*adminv1alpha1.CreateTokenResponse, error) {
	p, _ := auth.FromContext(ctx)
	if req.GetName() == "" || req.GetProject() == "" {
		return nil, status.Error(codes.InvalidArgument, "name and project are required")
	}
	role, err := auth.ParseRole(req.GetRole())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	plain, id, hash, err := auth.MintToken()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "mint token: %v", err)
	}
	rec := &store.TokenRecord{
		ID: id, Name: req.GetName(), Project: req.GetProject(),
		Role: role.String(), TokenHash: hash,
		CreatedBy: fmt.Sprintf("%s:%s", p.Type, p.Subject),
	}
	if err := a.store.InsertToken(ctx, rec); err != nil {
		return nil, status.Errorf(codes.Internal, "store token: %v", err)
	}
	stored, err := a.store.GetToken(ctx, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read back token: %v", err)
	}
	return &adminv1alpha1.CreateTokenResponse{Token: plain, Info: tokenInfo(stored)}, nil
}

func (a *adminService) ListTokens(ctx context.Context, req *adminv1alpha1.ListTokensRequest) (*adminv1alpha1.ListTokensResponse, error) {
	recs, err := a.store.ListTokens(ctx, req.GetProject())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list tokens: %v", err)
	}
	resp := &adminv1alpha1.ListTokensResponse{}
	for _, r := range recs {
		resp.Tokens = append(resp.Tokens, tokenInfo(r))
	}
	return resp, nil
}

func (a *adminService) RevokeToken(ctx context.Context, req *adminv1alpha1.RevokeTokenRequest) (*adminv1alpha1.RevokeTokenResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	found, err := a.store.RevokeToken(ctx, req.GetId())
	if err != nil {
		if errors.Is(err, store.ErrTokenNotFound) {
			return &adminv1alpha1.RevokeTokenResponse{Found: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "revoke token: %v", err)
	}
	return &adminv1alpha1.RevokeTokenResponse{Found: found}, nil
}

func tokenInfo(r *store.TokenRecord) *adminv1alpha1.TokenInfo {
	info := &adminv1alpha1.TokenInfo{
		Id: r.ID, Name: r.Name, Project: r.Project, Role: r.Role,
		CreatedBy: r.CreatedBy, CreatedAt: tsOrNil(&r.CreatedAt),
		LastUsedAt: tsOrNil(r.LastUsedAt), RevokedAt: tsOrNil(r.RevokedAt),
	}
	return info
}

func tsOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}

func projectToProto(p *store.ProjectRecord) *adminv1alpha1.Project {
	return &adminv1alpha1.Project{
		Tenant: p.Tenant, Org: p.Org, Name: p.Name, Weight: int32(p.Weight),
		CreatedAt: tsOrNil(&p.CreatedAt), ArchivedAt: tsOrNil(p.ArchivedAt),
	}
}

func (a *adminService) CreateProject(ctx context.Context, req *adminv1alpha1.CreateProjectRequest) (*adminv1alpha1.Project, error) {
	if req.GetTenant() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant is required")
	}
	p, err := a.store.EnsureProject(ctx, req.GetTenant())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create project: %v", err)
	}
	if w := int(req.GetWeight()); w >= 1 && w != p.Weight {
		if err := a.store.SetProjectWeight(ctx, p.Tenant, w); err != nil {
			return nil, status.Errorf(codes.Internal, "set weight: %v", err)
		}
		p.Weight = w
	}
	return projectToProto(p), nil
}

func (a *adminService) ListProjects(ctx context.Context, req *adminv1alpha1.ListProjectsRequest) (*adminv1alpha1.ListProjectsResponse, error) {
	recs, err := a.store.ListProjects(ctx, req.GetIncludeArchived())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list projects: %v", err)
	}
	resp := &adminv1alpha1.ListProjectsResponse{}
	for _, p := range recs {
		resp.Projects = append(resp.Projects, projectToProto(p))
	}
	return resp, nil
}

func (a *adminService) ArchiveProject(ctx context.Context, req *adminv1alpha1.ArchiveProjectRequest) (*adminv1alpha1.ArchiveProjectResponse, error) {
	if req.GetTenant() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant is required")
	}
	found, err := a.store.ArchiveProject(ctx, req.GetTenant())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "archive project: %v", err)
	}
	return &adminv1alpha1.ArchiveProjectResponse{Found: found}, nil
}

func (a *adminService) SetQuota(ctx context.Context, req *adminv1alpha1.SetQuotaRequest) (*adminv1alpha1.SetQuotaResponse, error) {
	if req.GetTenant() == "" || req.GetUnit() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant and unit are required")
	}
	if _, err := a.store.GetProject(ctx, req.GetTenant()); err != nil {
		if errors.Is(err, store.ErrProjectNotFound) {
			return nil, status.Errorf(codes.NotFound, "project %q not found", req.GetTenant())
		}
		return nil, status.Errorf(codes.Internal, "project lookup: %v", err)
	}
	if err := a.store.SetQuota(ctx, req.GetTenant(), req.GetUnit(), req.GetLimit()); err != nil {
		return nil, status.Errorf(codes.Internal, "set quota: %v", err)
	}
	return &adminv1alpha1.SetQuotaResponse{}, nil
}

func (a *adminService) ListQuotas(ctx context.Context, req *adminv1alpha1.ListQuotasRequest) (*adminv1alpha1.ListQuotasResponse, error) {
	recs, err := a.store.ListQuotas(ctx, req.GetTenant())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list quotas: %v", err)
	}
	resp := &adminv1alpha1.ListQuotasResponse{}
	for _, q := range recs {
		resp.Quotas = append(resp.Quotas, &adminv1alpha1.Quota{
			Tenant: q.Tenant, Unit: q.Unit, Limit: q.Limit,
		})
	}
	return resp, nil
}
