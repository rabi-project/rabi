// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthorize(t *testing.T) {
	const key = "secret-key"
	cases := []struct {
		name string
		md   metadata.MD
		want codes.Code
	}{
		{"no metadata at all", nil, codes.Unauthenticated},
		{"missing header", metadata.MD{}, codes.Unauthenticated},
		{"valid bearer", metadata.Pairs("authorization", "Bearer "+key), codes.OK},
		{"valid via gateway prefix", metadata.Pairs("grpcgateway-authorization", "Bearer "+key), codes.OK},
		{"wrong key", metadata.Pairs("authorization", "Bearer nope"), codes.Unauthenticated},
		{"no bearer prefix", metadata.Pairs("authorization", key), codes.Unauthenticated},
		{"empty bearer", metadata.Pairs("authorization", "Bearer "), codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.md)
			}
			err := authorize(ctx, key)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("authorize() code = %v, want %v (err=%v)", got, tc.want, err)
			}
		})
	}
}
