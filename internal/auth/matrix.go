// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// minRole is the authorization matrix: full gRPC method name → minimum role.
// Every method served by the control plane MUST have an entry — the matrix
// test auto-enumerates methods from the proto registry and fails on any gap,
// and Authorize fails closed on methods it does not know.
//
// Rationale (docs/decisions.md D-033): reads are viewer+, job mutations are
// member+, token inventory is operator+, token lifecycle is admin-only.
var minRole = map[string]Role{
	// tangle.api.v1alpha1 — the spec surface.
	"/tangle.api.v1alpha1.JobsService/SubmitJob":       RoleMember,
	"/tangle.api.v1alpha1.JobsService/GetJob":          RoleViewer,
	"/tangle.api.v1alpha1.JobsService/ListJobs":        RoleViewer,
	"/tangle.api.v1alpha1.JobsService/WatchJob":        RoleViewer,
	"/tangle.api.v1alpha1.JobsService/CancelJob":       RoleMember,
	"/tangle.api.v1alpha1.TargetsService/ListTargets":  RoleViewer,
	"/tangle.api.v1alpha1.TargetsService/GetTarget":    RoleViewer,
	"/tangle.api.v1alpha1.UsageService/GetTenantUsage": RoleViewer,

	// rabi.admin.v1alpha1 — implementation-defined admin surface (D-033).
	"/rabi.admin.v1alpha1.AdminService/WhoAmI":      RoleViewer,
	"/rabi.admin.v1alpha1.AdminService/ListTokens":  RoleOperator,
	"/rabi.admin.v1alpha1.AdminService/CreateToken": RoleAdmin,
	"/rabi.admin.v1alpha1.AdminService/RevokeToken": RoleAdmin,

	// Project lifecycle + quotas (M2): reads operator+, mutations admin.
	"/rabi.admin.v1alpha1.AdminService/CreateProject":  RoleAdmin,
	"/rabi.admin.v1alpha1.AdminService/ListProjects":   RoleOperator,
	"/rabi.admin.v1alpha1.AdminService/ArchiveProject": RoleAdmin,
	"/rabi.admin.v1alpha1.AdminService/SetQuota":       RoleAdmin,
	"/rabi.admin.v1alpha1.AdminService/ListQuotas":     RoleOperator,

	// Accounting (M3): ledger export is a read for operators and viewers'
	// own projects alike — viewer suffices, project scope gates the data.
	"/rabi.admin.v1alpha1.AdminService/ExportLedger": RoleViewer,
}

// MinRole reports the minimum role for a full method name.
func MinRole(fullMethod string) (Role, bool) {
	r, ok := minRole[fullMethod]
	return r, ok
}

// Authorize checks p against the matrix. Unknown methods are denied (fail
// closed): shipping a new RPC requires a matrix entry, enforced by test.
func Authorize(p Principal, fullMethod string) error {
	want, ok := minRole[fullMethod]
	if !ok {
		return status.Errorf(codes.PermissionDenied,
			"method %s has no authorization matrix entry", fullMethod)
	}
	if p.Role < want {
		return status.Errorf(codes.PermissionDenied,
			"%s requires role %s or higher; %s %q has role %s",
			fullMethod, want, p.Type, p.Name, p.Role)
	}
	return nil
}
