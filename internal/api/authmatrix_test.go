// SPDX-License-Identifier: Apache-2.0

// The Phase 1 authz matrix (test-and-verification-plan.md §3): every
// endpoint × every role, auto-enumerated from the proto registry so a new
// RPC cannot ship untested, with an audit entry asserted for denied calls.
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

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

// servedMethods enumerates every RPC the control plane serves, from the
// descriptors of the registered proto files.
func servedMethods(t *testing.T) []protoreflect.MethodDescriptor {
	t.Helper()
	var out []protoreflect.MethodDescriptor
	for _, file := range []protoreflect.FileDescriptor{
		apiv1alpha1.File_tangle_api_v1alpha1_jobs_proto,
		adminv1alpha1.File_rabi_admin_v1alpha1_admin_proto,
	} {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				out = append(out, methods.Get(j))
			}
		}
	}
	if len(out) < 12 {
		t.Fatalf("enumerated only %d endpoints; expected the full API surface", len(out))
	}
	return out
}

func fullMethodName(m protoreflect.MethodDescriptor) string {
	return fmt.Sprintf("/%s/%s", m.Parent().FullName(), m.Name())
}

// mintRoleToken creates a real stored API token with the given role.
func mintRoleToken(t *testing.T, role auth.Role) string {
	t.Helper()
	plain, id, hash, err := auth.MintToken()
	if err != nil {
		t.Fatal(err)
	}
	err = testStore.InsertToken(t.Context(), &store.TokenRecord{
		ID: id, Name: "matrix-" + role.String(), Project: "matrix-project",
		Role: role.String(), TokenHash: hash, CreatedBy: "authmatrix_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// TestAuthzMatrix asserts 100% of endpoint × role cells: roles at or above
// the matrix minimum must pass authorization (any later failure must not be
// an auth code), roles below must get PermissionDenied, and every deny must
// land in the audit log. Unauthenticated callers are a fourth row per
// endpoint.
func TestAuthzMatrix(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	tokens := map[auth.Role]string{}
	for _, r := range auth.Roles() {
		tokens[r] = mintRoleToken(t, r)
	}

	methods := servedMethods(t)
	cells, denies := 0, 0
	for _, m := range methods {
		name := fullMethodName(m)
		min, ok := auth.MinRole(name)
		if !ok {
			t.Fatalf("method %s served but missing from the authorization matrix", name)
		}
		for _, role := range auth.Roles() {
			cells++
			t.Run(fmt.Sprintf("%s/%s", name, role), func(t *testing.T) {
				ctx := metadata.AppendToOutgoingContext(t.Context(),
					"authorization", "Bearer "+tokens[role])
				err := invoke(ctx, conn, m)
				code := status.Code(err)
				if role >= min {
					if code == codes.PermissionDenied || code == codes.Unauthenticated {
						t.Fatalf("role %s must pass authz on %s, got %v", role, name, err)
					}
				} else {
					if code != codes.PermissionDenied {
						t.Fatalf("role %s must be denied on %s, got %v (code %v)", role, name, err, code)
					}
				}
			})
			if role < min {
				denies++
			}
		}
		t.Run(name+"/unauthenticated", func(t *testing.T) {
			err := invoke(t.Context(), conn, m)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("unauthenticated call must fail closed, got %v", err)
			}
		})
	}
	if cells != len(methods)*len(auth.Roles()) {
		t.Fatalf("asserted %d cells, want %d", cells, len(methods)*len(auth.Roles()))
	}

	// Every denied cell (and every unauthenticated probe) is audited.
	entries, err := testStore.AuditEntries(t.Context(), "deny", 10_000)
	if err != nil {
		t.Fatal(err)
	}
	denied := map[string]int{}
	anonymous := 0
	for _, e := range entries {
		if e.PrincipalType == "anonymous" {
			anonymous++
			continue
		}
		denied[e.Method+"|"+e.Role]++
	}
	for _, m := range methods {
		name := fullMethodName(m)
		min, _ := auth.MinRole(name)
		for _, role := range auth.Roles() {
			if role < min && denied[name+"|"+role.String()] == 0 {
				t.Errorf("no audit entry for denied cell %s × %s", name, role)
			}
		}
	}
	if anonymous < len(methods) {
		t.Errorf("anonymous denials audited %d times, want ≥ %d", anonymous, len(methods))
	}
	if denies == 0 {
		t.Fatal("matrix produced no denied cells; suite is vacuous")
	}
}

// TestRevokedAndBogusTokens: revocation takes effect immediately and
// unknown/tampered tokens never authenticate.
func TestRevokedAndBogusTokens(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	listTargets := func(bearer string) error {
		ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+bearer)
		client := apiv1alpha1.NewTargetsServiceClient(conn)
		_, err := client.ListTargets(ctx, &apiv1alpha1.ListTargetsRequest{})
		return err
	}

	tok := mintRoleToken(t, auth.RoleViewer)
	if err := listTargets(tok); err != nil {
		t.Fatalf("fresh viewer token must work: %v", err)
	}
	id, err := auth.TokenID(tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.RevokeToken(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if code := status.Code(listTargets(tok)); code != codes.Unauthenticated {
		t.Fatalf("revoked token must be Unauthenticated, got %v", code)
	}
	if code := status.Code(listTargets("rabi_ffffffffffff_deadbeef")); code != codes.Unauthenticated {
		t.Fatalf("unknown token must be Unauthenticated, got %v", code)
	}
	// Right id, wrong secret: hash comparison must fail.
	tampered := tok[:len(tok)-4] + "0000"
	if tampered == tok {
		tampered = tok[:len(tok)-4] + "1111"
	}
	if code := status.Code(listTargets(tampered)); code != codes.Unauthenticated {
		t.Fatalf("tampered token must be Unauthenticated, got %v", code)
	}
}

// TestAdminActionsAudited: allow-side audit entries for token lifecycle.
func TestAdminActionsAudited(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)
	client := adminv1alpha1.NewAdminServiceClient(conn)

	created, err := client.CreateToken(ctx, &adminv1alpha1.CreateTokenRequest{
		Name: "audited", Project: "matrix-project", Role: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.RevokeToken(ctx, &adminv1alpha1.RevokeTokenRequest{Id: created.GetInfo().GetId()}); err != nil {
		t.Fatal(err)
	}

	entries, err := testStore.AuditEntries(t.Context(), "allow", 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawCreate, sawRevoke bool
	for _, e := range entries {
		switch e.Method {
		case "/rabi.admin.v1alpha1.AdminService/CreateToken":
			sawCreate = true
		case "/rabi.admin.v1alpha1.AdminService/RevokeToken":
			sawRevoke = true
		}
	}
	if !sawCreate || !sawRevoke {
		t.Fatalf("admin actions missing from audit log: create=%v revoke=%v", sawCreate, sawRevoke)
	}
}

// invoke calls a method generically with an empty request message.
func invoke(ctx context.Context, conn *grpc.ClientConn, m protoreflect.MethodDescriptor) error {
	fullMethod := fullMethodName(m)
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
