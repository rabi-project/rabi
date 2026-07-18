// SPDX-License-Identifier: Apache-2.0

// Package auth is the Phase 1 AuthN/Z core: principals (OIDC users, API
// tokens, the bootstrap token), the four roles, and the per-method
// authorization matrix. The API layer authenticates every call to a
// Principal, authorizes it against the matrix, and records an audit entry
// for every denial and every admin action (phase1-build-plan.md M1).
package auth

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Role is an ordered privilege level. Higher includes lower.
type Role int

const (
	RoleViewer Role = iota
	RoleMember
	RoleOperator
	RoleAdmin
)

var roleNames = map[Role]string{
	RoleViewer:   "viewer",
	RoleMember:   "member",
	RoleOperator: "operator",
	RoleAdmin:    "admin",
}

func (r Role) String() string {
	if n, ok := roleNames[r]; ok {
		return n
	}
	return fmt.Sprintf("role(%d)", int(r))
}

// ParseRole maps the wire/storage name to a Role.
func ParseRole(s string) (Role, error) {
	for r, n := range roleNames {
		if n == s {
			return r, nil
		}
	}
	return 0, fmt.Errorf("unknown role %q (want viewer|member|operator|admin)", s)
}

// Roles lists every role, lowest privilege first (matrix tests iterate this).
func Roles() []Role {
	return []Role{RoleViewer, RoleMember, RoleOperator, RoleAdmin}
}

// PrincipalType distinguishes how a caller authenticated.
type PrincipalType string

const (
	PrincipalOIDC      PrincipalType = "oidc"
	PrincipalToken     PrincipalType = "token"
	PrincipalBootstrap PrincipalType = "bootstrap"
)

// Principal is an authenticated caller.
type Principal struct {
	Type    PrincipalType
	Subject string // OIDC "iss|sub", token id, or "bootstrap"
	Name    string // human-facing: email/preferred_username or token name
	Role    Role
	// Project scopes token principals to one project (Phase 0 tenant
	// string). Empty means unscoped: OIDC users stay unscoped until the
	// M2 org/project hierarchy assigns memberships (docs/decisions.md D-034).
	Project string
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p; handlers read it via FromContext.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the authenticated principal, if the interceptor set one.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// CheckProject enforces a token's project scope against a resource's
// tenant/project. Unscoped principals (OIDC until M2, bootstrap) pass.
func CheckProject(ctx context.Context, project string) error {
	p, ok := FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "no principal in context")
	}
	if p.Project != "" && p.Project != project {
		return status.Errorf(codes.PermissionDenied,
			"token is scoped to project %q, not %q", p.Project, project)
	}
	return nil
}
