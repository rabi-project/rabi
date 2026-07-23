// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

// buildAuthenticator wires Phase 1 auth from the environment:
//
//	RABI_BOOTSTRAP_TOKEN  admin credential for dev/compose and first-admin
//	                      setup (mint real tokens, then rotate it away)
//	RABI_OIDC_ISSUER      OIDC issuer URL (enables OIDC when set)
//	RABI_OIDC_CLIENT_ID   required audience
//	RABI_OIDC_GROUPS_CLAIM claim carrying group names (default "groups")
//	RABI_OIDC_GROUP_ROLES "group=role,group=role" map; default
//	                      rabi-admin/operator/member/viewer=matching role
//	RABI_OIDC_DEFAULT_ROLE role with no mapped group (default viewer)
//
// At least one of bootstrap/OIDC must be configured.
func buildAuthenticator(ctx context.Context, st *store.Store, logger *slog.Logger) (*api.Authenticator, error) {
	var verifier *auth.OIDCVerifier
	if issuer := os.Getenv("RABI_OIDC_ISSUER"); issuer != "" {
		groupRoles, err := parseGroupRoles(os.Getenv("RABI_OIDC_GROUP_ROLES"))
		if err != nil {
			return nil, err
		}
		defaultRole := auth.RoleViewer
		if v := os.Getenv("RABI_OIDC_DEFAULT_ROLE"); v != "" {
			if defaultRole, err = auth.ParseRole(v); err != nil {
				return nil, fmt.Errorf("RABI_OIDC_DEFAULT_ROLE: %w", err)
			}
		}
		verifier, err = auth.NewOIDCVerifier(ctx, auth.OIDCConfig{
			Issuer:      issuer,
			ClientID:    os.Getenv("RABI_OIDC_CLIENT_ID"),
			GroupsClaim: os.Getenv("RABI_OIDC_GROUPS_CLAIM"),
			GroupRoles:  groupRoles,
			DefaultRole: defaultRole,
		})
		if err != nil {
			return nil, err
		}
		logger.Info("oidc enabled", "issuer", issuer)
	}
	if tok := os.Getenv("RABI_BOOTSTRAP_TOKEN"); tok != "" {
		logger.Info("bootstrap token enabled (dev/first-admin only; rotate to real tokens)")
	}
	return api.NewAuthenticator(os.Getenv("RABI_BOOTSTRAP_TOKEN"), verifier, st, logger)
}

func parseGroupRoles(spec string) (map[string]auth.Role, error) {
	if spec == "" {
		return map[string]auth.Role{
			"rabi-admin":    auth.RoleAdmin,
			"rabi-operator": auth.RoleOperator,
			"rabi-member":   auth.RoleMember,
			"rabi-viewer":   auth.RoleViewer,
		}, nil
	}
	out := map[string]auth.Role{}
	for _, pair := range strings.Split(spec, ",") {
		group, roleName, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || group == "" {
			return nil, fmt.Errorf("RABI_OIDC_GROUP_ROLES: bad entry %q (want group=role)", pair)
		}
		role, err := auth.ParseRole(roleName)
		if err != nil {
			return nil, fmt.Errorf("RABI_OIDC_GROUP_ROLES: %w", err)
		}
		out[group] = role
	}
	return out, nil
}
