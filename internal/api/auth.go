// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

// Phase 1 authentication (M1): every call presents "Authorization: Bearer
// <credential>", where the credential is one of
//   - the bootstrap token (RABI_BOOTSTRAP_TOKEN — dev/first-admin only),
//   - a per-project API token ("rabi_<id>_<secret>", hash-verified), or
//   - an OIDC JWT verified against the configured IdP.
// The static RABI_API_KEY path is deleted. Authorization is the role matrix
// in internal/auth; every deny and every admin action is audited.
const (
	authHeader        = "authorization"
	gatewayAuthHeader = "grpcgateway-authorization"
)

// adminActions are the allow-side calls that must always be audited
// (phase1-build-plan.md M1: "every admin action").
var adminActions = map[string]bool{
	"/rabi.admin.v1alpha1.AdminService/CreateToken": true,
	"/rabi.admin.v1alpha1.AdminService/RevokeToken": true,
}

// Authenticator resolves a bearer credential to a Principal.
type Authenticator struct {
	// bootstrapHash is the SHA-256 of the bootstrap token; empty disables it.
	bootstrapHash [32]byte
	hasBootstrap  bool
	oidc          *auth.OIDCVerifier // nil disables OIDC
	store         *store.Store
	logger        *slog.Logger
}

// NewAuthenticator wires the three credential paths. At least one of
// bootstrap/OIDC must be configured or nobody could ever mint a token.
func NewAuthenticator(bootstrapToken string, oidc *auth.OIDCVerifier, st *store.Store, logger *slog.Logger) (*Authenticator, error) {
	if bootstrapToken == "" && oidc == nil {
		return nil, errors.New("auth: configure RABI_OIDC_ISSUER and/or RABI_BOOTSTRAP_TOKEN")
	}
	a := &Authenticator{oidc: oidc, store: st, logger: logger}
	if bootstrapToken != "" {
		a.bootstrapHash = sha256.Sum256([]byte(bootstrapToken))
		a.hasBootstrap = true
	}
	return a, nil
}

func bearerFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, v := range append(md.Get(authHeader), md.Get(gatewayAuthHeader)...) {
		if tok, found := strings.CutPrefix(v, "Bearer "); found && tok != "" {
			return tok
		}
	}
	return ""
}

// Authenticate resolves the caller or returns Unauthenticated.
func (a *Authenticator) Authenticate(ctx context.Context) (auth.Principal, error) {
	bearer := bearerFromContext(ctx)
	if bearer == "" {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "missing credentials")
	}

	if a.hasBootstrap {
		sum := sha256.Sum256([]byte(bearer))
		if subtle.ConstantTimeCompare(sum[:], a.bootstrapHash[:]) == 1 {
			return auth.Principal{
				Type: auth.PrincipalBootstrap, Subject: "bootstrap",
				Name: "bootstrap", Role: auth.RoleAdmin,
			}, nil
		}
	}

	if auth.IsToken(bearer) {
		return a.authenticateToken(ctx, bearer)
	}

	if a.oidc != nil {
		p, err := a.oidc.Verify(ctx, bearer)
		if err != nil {
			return auth.Principal{}, status.Errorf(codes.Unauthenticated, "invalid credentials: %v", err)
		}
		return p, nil
	}
	return auth.Principal{}, status.Error(codes.Unauthenticated, "invalid credentials")
}

func (a *Authenticator) authenticateToken(ctx context.Context, bearer string) (auth.Principal, error) {
	id, err := auth.TokenID(bearer)
	if err != nil {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "invalid credentials")
	}
	rec, err := a.store.GetToken(ctx, id)
	if errors.Is(err, store.ErrTokenNotFound) {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "invalid credentials")
	}
	if err != nil {
		return auth.Principal{}, status.Errorf(codes.Internal, "token lookup: %v", err)
	}
	if !auth.VerifyTokenHash(bearer, rec.TokenHash) {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "invalid credentials")
	}
	if rec.RevokedAt != nil {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "token revoked")
	}
	role, err := auth.ParseRole(rec.Role)
	if err != nil {
		return auth.Principal{}, status.Errorf(codes.Internal, "stored token role: %v", err)
	}
	a.store.TouchToken(ctx, rec.ID)
	return auth.Principal{
		Type: auth.PrincipalToken, Subject: rec.ID, Name: rec.Name,
		Role: role, Project: rec.Project,
	}, nil
}

// check runs authn + authz for one call, records audit entries, and returns
// the principal-carrying context. Audit writes are best-effort: the decision
// stands even if the insert fails (logged), so an audit outage cannot take
// down the API. Denials are always audited; allows only for admin actions.
func (a *Authenticator) check(ctx context.Context, fullMethod string) (context.Context, error) {
	p, err := a.Authenticate(ctx)
	if err != nil {
		a.audit(store.AuditEntry{
			PrincipalType: "anonymous", Subject: "-",
			Method: fullMethod, Decision: "deny", Reason: status.Convert(err).Message(),
		})
		return nil, err
	}
	if err := auth.Authorize(p, fullMethod); err != nil {
		a.audit(store.AuditEntry{
			PrincipalType: string(p.Type), Subject: p.Subject, PrincipalName: p.Name,
			Role: p.Role.String(), Method: fullMethod, Decision: "deny",
			Reason: status.Convert(err).Message(),
		})
		return nil, err
	}
	if adminActions[fullMethod] {
		a.audit(store.AuditEntry{
			PrincipalType: string(p.Type), Subject: p.Subject, PrincipalName: p.Name,
			Role: p.Role.String(), Method: fullMethod, Decision: "allow",
		})
	}
	return auth.WithPrincipal(ctx, p), nil
}

func (a *Authenticator) audit(e store.AuditEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.store.RecordAudit(ctx, e); err != nil {
		a.logger.Error("audit write failed", "method", e.Method, "decision", e.Decision, "err", err)
	}
}

// UnaryAuthInterceptor authenticates + authorizes every unary RPC.
func (a *Authenticator) UnaryAuthInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := a.check(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor authenticates + authorizes every streaming RPC.
func (a *Authenticator) StreamAuthInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := a.check(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &principalStream{ServerStream: ss, ctx: ctx})
	}
}

// principalStream overrides Context so stream handlers see the principal.
type principalStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *principalStream) Context() context.Context { return s.ctx }
