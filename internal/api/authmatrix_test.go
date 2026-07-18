// SPDX-License-Identifier: Apache-2.0

// T1.authkey: wrong/missing key → Unauthenticated on every endpoint. The
// endpoint list is derived from the proto descriptors, so a new RPC is
// covered automatically.
package api_test

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
)

func TestAuthMatrix(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	credentials := []struct {
		name string
		md   metadata.MD
	}{
		{"missing key", nil},
		{"wrong key", metadata.Pairs("authorization", "Bearer wrong-key")},
		{"malformed header", metadata.Pairs("authorization", testAPIKey)}, // no Bearer prefix
	}

	services := apiv1alpha1.File_tangle_api_v1alpha1_jobs_proto.Services()
	total := 0
	for i := 0; i < services.Len(); i++ {
		svc := services.Get(i)
		methods := svc.Methods()
		for j := 0; j < methods.Len(); j++ {
			m := methods.Get(j)
			total++
			for _, cred := range credentials {
				name := fmt.Sprintf("%s/%s/%s", svc.Name(), m.Name(), cred.name)
				t.Run(name, func(t *testing.T) {
					ctx := t.Context()
					if cred.md != nil {
						ctx = metadata.NewOutgoingContext(ctx, cred.md)
					}
					err := invoke(ctx, conn, m)
					if status.Code(err) != codes.Unauthenticated {
						t.Fatalf("code = %v, want Unauthenticated (err=%v)", status.Code(err), err)
					}
				})
			}
		}
	}
	if total < 8 {
		t.Fatalf("enumerated only %d endpoints; expected the full API surface", total)
	}
}

// invoke calls a method generically with an empty request message.
func invoke(ctx context.Context, conn *grpc.ClientConn, m protoreflect.MethodDescriptor) error {
	fullMethod := fmt.Sprintf("/%s/%s", m.Parent().FullName(), m.Name())
	req := dynamicpb.NewMessage(m.Input())
	resp := dynamicpb.NewMessage(m.Output())

	if m.IsStreamingServer() || m.IsStreamingClient() {
		desc := &grpc.StreamDesc{
			StreamName:    string(m.Name()),
			ServerStreams: m.IsStreamingServer(),
			ClientStreams: m.IsStreamingClient(),
		}
		stream, err := conn.NewStream(ctx, desc, fullMethod)
		if err != nil {
			return err
		}
		if err := stream.SendMsg(req); err != nil {
			return err
		}
		if err := stream.CloseSend(); err != nil {
			return err
		}
		return stream.RecvMsg(resp)
	}
	return conn.Invoke(ctx, fullMethod, req, resp)
}
