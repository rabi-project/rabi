// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRoleOrderingAndParse(t *testing.T) {
	if RoleViewer >= RoleMember || RoleMember >= RoleOperator || RoleOperator >= RoleAdmin {
		t.Fatal("role ordering broken")
	}
	for _, r := range Roles() {
		got, err := ParseRole(r.String())
		if err != nil || got != r {
			t.Fatalf("ParseRole(%q) = %v, %v", r.String(), got, err)
		}
	}
	if _, err := ParseRole("root"); err == nil {
		t.Fatal("ParseRole accepted unknown role")
	}
}

func TestAuthorizeMatrix(t *testing.T) {
	cases := []struct {
		method string
		min    Role
	}{
		{"/tangle.api.v1alpha1.JobsService/SubmitJob", RoleMember},
		{"/tangle.api.v1alpha1.JobsService/GetJob", RoleViewer},
		{"/rabi.admin.v1alpha1.AdminService/CreateToken", RoleAdmin},
		{"/rabi.admin.v1alpha1.AdminService/ListTokens", RoleOperator},
	}
	for _, c := range cases {
		for _, r := range Roles() {
			err := Authorize(Principal{Role: r, Type: PrincipalOIDC, Name: "t"}, c.method)
			if r >= c.min && err != nil {
				t.Errorf("%s as %s: unexpected deny: %v", c.method, r, err)
			}
			if r < c.min {
				if status.Code(err) != codes.PermissionDenied {
					t.Errorf("%s as %s: want PermissionDenied, got %v", c.method, r, err)
				}
			}
		}
	}
}

func TestAuthorizeFailsClosedOnUnknownMethod(t *testing.T) {
	err := Authorize(Principal{Role: RoleAdmin}, "/rabi.admin.v1alpha1.AdminService/DropTables")
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unknown method must be denied even for admin, got %v", err)
	}
}

func TestTokenMintVerifyRoundTrip(t *testing.T) {
	plain, id, hash, err := MintToken()
	if err != nil {
		t.Fatal(err)
	}
	if !IsToken(plain) {
		t.Fatalf("minted token %q lacks prefix", plain)
	}
	gotID, err := TokenID(plain)
	if err != nil || gotID != id {
		t.Fatalf("TokenID = %q, %v; want %q", gotID, err, id)
	}
	if !VerifyTokenHash(plain, hash) {
		t.Fatal("minted token fails against its own hash")
	}
	if VerifyTokenHash(plain+"x", hash) {
		t.Fatal("tampered token verified")
	}
	if strings.Contains(hash, plain[len(plain)-10:]) {
		t.Fatal("hash appears to embed the secret")
	}
}

func TestCheckProject(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{Type: PrincipalToken, Project: "alice", Role: RoleMember})
	if err := CheckProject(ctx, "alice"); err != nil {
		t.Fatalf("same-project check failed: %v", err)
	}
	if err := CheckProject(ctx, "bob"); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-project must deny, got %v", err)
	}
	unscoped := WithPrincipal(context.Background(), Principal{Type: PrincipalOIDC, Role: RoleAdmin})
	if err := CheckProject(unscoped, "anyone"); err != nil {
		t.Fatalf("unscoped principal must pass: %v", err)
	}
	if err := CheckProject(context.Background(), "x"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing principal must be Unauthenticated, got %v", err)
	}
}
