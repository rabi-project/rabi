// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The MVP has exactly one auth path: a static API key from TANGLE_API_KEY,
// presented as "Authorization: Bearer <key>" (mvp-build-plan.md §2). The REST
// gateway forwards the header under grpc-gateway's permanent-header prefix,
// so both metadata keys are accepted.
const (
	authHeader        = "authorization"
	gatewayAuthHeader = "grpcgateway-authorization"
)

func authorize(ctx context.Context, key string) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing credentials")
	}
	values := append(md.Get(authHeader), md.Get(gatewayAuthHeader)...)
	for _, v := range values {
		tok, found := strings.CutPrefix(v, "Bearer ")
		if found && subtle.ConstantTimeCompare([]byte(tok), []byte(key)) == 1 {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "invalid or missing API key")
}

// UnaryAuthInterceptor enforces the static API key on every unary RPC.
func UnaryAuthInterceptor(key string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authorize(ctx, key); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor enforces the static API key on every streaming RPC.
func StreamAuthInterceptor(key string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authorize(ss.Context(), key); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
