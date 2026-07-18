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
