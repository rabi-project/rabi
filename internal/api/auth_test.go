// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/rabi-project/rabi/internal/auth"
)

// Unit coverage of the credential paths that need no database: bootstrap
// token handling and header parsing. Token/OIDC paths are covered by the
// component + matrix suites and the dex e2e.
func TestAuthenticateBootstrap(t *testing.T) {
	const boot = "bootstrap-secret"
	a, err := NewAuthenticator(boot, nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		md       metadata.MD
		want     codes.Code
		wantRole auth.Role
	}{
		{"no metadata at all", nil, codes.Unauthenticated, 0},
		{"missing header", metadata.MD{}, codes.Unauthenticated, 0},
		{"valid bootstrap", metadata.Pairs("authorization", "Bearer "+boot), codes.OK, auth.RoleAdmin},
		{"valid via gateway prefix", metadata.Pairs("grpcgateway-authorization", "Bearer "+boot), codes.OK, auth.RoleAdmin},
		{"wrong credential, not a token, no oidc", metadata.Pairs("authorization", "Bearer nope"), codes.Unauthenticated, 0},
		{"no bearer prefix", metadata.Pairs("authorization", boot), codes.Unauthenticated, 0},
		{"empty bearer", metadata.Pairs("authorization", "Bearer "), codes.Unauthenticated, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.md)
			}
			p, err := a.Authenticate(ctx)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("Authenticate() code = %v, want %v (err=%v)", got, tc.want, err)
			}
			if err == nil {
				if p.Type != auth.PrincipalBootstrap || p.Role != tc.wantRole {
					t.Fatalf("principal = %+v, want bootstrap admin", p)
				}
			}
		})
	}
}

func TestNewAuthenticatorRequiresSomePath(t *testing.T) {
	if _, err := NewAuthenticator("", nil, nil, slog.Default()); err == nil {
		t.Fatal("no bootstrap and no OIDC must be a configuration error")
	}
}
