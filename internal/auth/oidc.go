// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCConfig configures JWT verification against one spec-compliant IdP
// (phase1-build-plan.md §2: OIDC only, via coreos/go-oidc).
type OIDCConfig struct {
	Issuer      string
	ClientID    string          // required audience of accepted tokens
	GroupsClaim string          // claim holding group names; default "groups"
	GroupRoles  map[string]Role // group name → role
	DefaultRole Role            // role for authenticated users with no mapped group
}

// OIDCVerifier authenticates bearer JWTs. Construction performs issuer
// discovery, so it needs the IdP reachable once at startup; verification
// afterwards uses the cached JWKS (refreshed on unknown key ids).
type OIDCVerifier struct {
	cfg      OIDCConfig
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier discovers the issuer and prepares verification.
func NewOIDCVerifier(ctx context.Context, cfg OIDCConfig) (*OIDCVerifier, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("oidc: issuer and client id are required")
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %s: %w", cfg.Issuer, err)
	}
	return &OIDCVerifier{
		cfg:      cfg,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

// Verify authenticates a raw JWT and maps claims to a Principal. The role is
// the highest one granted by any mapped group; users with no mapped group get
// DefaultRole (deployments that want deny-by-default simply map no groups and
// leave DefaultRole at viewer — viewers can read, never mutate).
func (v *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Principal, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("oidc: %w", err)
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("oidc: decode claims: %w", err)
	}

	role := v.cfg.DefaultRole
	if raw, ok := claims[v.cfg.GroupsClaim].([]any); ok {
		for _, g := range raw {
			name, _ := g.(string)
			if r, ok := v.cfg.GroupRoles[name]; ok && r > role {
				role = r
			}
		}
	}

	name := idToken.Subject
	for _, k := range []string{"email", "preferred_username", "name"} {
		if s, ok := claims[k].(string); ok && s != "" {
			name = s
			break
		}
	}
	return Principal{
		Type:    PrincipalOIDC,
		Subject: idToken.Issuer + "|" + idToken.Subject,
		Name:    name,
		Role:    role,
	}, nil
}
