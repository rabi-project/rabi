// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	"github.com/rabi-project/rabi/internal/accounting"
	"github.com/rabi-project/rabi/internal/store"
)

// newProjectCmd groups project lifecycle (create/list/archive) — M2.
func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects (org/project tenancy)",
	}
	cmd.AddCommand(newProjectCreateCmd(), newProjectListCmd(), newProjectArchiveCmd())
	return cmd
}

func newProjectCreateCmd() *cobra.Command {
	var weight int32
	cmd := &cobra.Command{
		Use:   "create <tenant>",
		Short: "Create a project (tenant string, e.g. acme/qa)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			p, err := adminv1alpha1.NewAdminServiceClient(conn).CreateProject(ctx, &adminv1alpha1.CreateProjectRequest{
				Tenant: args[0], Weight: weight,
			})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(p)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project %s (org %s, name %s, weight %d)\n",
				p.GetTenant(), p.GetOrg(), p.GetName(), p.GetWeight())
			return nil
		},
	}
	cmd.Flags().Int32Var(&weight, "weight", 0, "fair-share weight (default 1)")
	return cmd
}

func newProjectListCmd() *cobra.Command {
	var includeArchived bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).ListProjects(ctx, &adminv1alpha1.ListProjectsRequest{
				IncludeArchived: includeArchived,
			})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "TENANT\tORG\tPROJECT\tWEIGHT\tCREATED\tSTATUS")
			for _, p := range resp.GetProjects() {
				status := "active"
				if p.GetArchivedAt() != nil {
					status = "archived " + p.GetArchivedAt().AsTime().Format(time.DateOnly)
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
					p.GetTenant(), p.GetOrg(), p.GetName(), p.GetWeight(),
					p.GetCreatedAt().AsTime().Format(time.DateOnly), status)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&includeArchived, "all", false, "include archived projects")
	return cmd
}

func newProjectArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <tenant>",
		Short: "Archive a project (stops new submissions; history remains)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).ArchiveProject(ctx, &adminv1alpha1.ArchiveProjectRequest{Tenant: args[0]})
			if err != nil {
				return err
			}
			if !resp.GetFound() {
				return fmt.Errorf("project %q not found", args[0])
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project %s archived\n", args[0])
			return nil
		},
	}
}

// newQuotaCmd groups quota management (set/list/remove) — M2.
func newQuotaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Manage per-project native-unit quotas",
	}
	cmd.AddCommand(newQuotaSetCmd(), newQuotaListCmd())
	return cmd
}

func newQuotaSetCmd() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "set <tenant> <unit> [limit]",
		Short: "Set (or with --remove, delete) a native-unit quota",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit := -1.0
			if !remove {
				if len(args) != 3 {
					return fmt.Errorf("limit required unless --remove")
				}
				if _, err := fmt.Sscanf(args[2], "%g", &limit); err != nil || limit < 0 {
					return fmt.Errorf("limit must be a non-negative number")
				}
			}
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			_, err = adminv1alpha1.NewAdminServiceClient(conn).SetQuota(ctx, &adminv1alpha1.SetQuotaRequest{
				Tenant: args[0], Unit: args[1], Limit: limit,
			})
			if err != nil {
				return err
			}
			if remove {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "quota %s/%s removed\n", args[0], args[1])
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "quota %s/%s = %g\n", args[0], args[1], limit)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the quota instead of setting it")
	return cmd
}

func newQuotaListCmd() *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List quotas",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).ListQuotas(ctx, &adminv1alpha1.ListQuotasRequest{Tenant: tenant})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "TENANT\tUNIT\tLIMIT")
			for _, q := range resp.GetQuotas() {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%g\n", q.GetTenant(), q.GetUnit(), q.GetLimit())
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&tenant, "project", "", "filter by project tenant string")
	return cmd
}

// newUsageExportCmd normalizes the immutable ledger under a site policy
// file and writes canonical CSV to stdout (M3). Same ledger + same policy
// version → byte-equal output.
func newUsageExportCmd() *cobra.Command {
	var project, policyPath string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the usage ledger as normalized cost records (CSV)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := os.ReadFile(policyPath)
			if err != nil {
				return err
			}
			policy, err := accounting.ParsePolicy(raw)
			if err != nil {
				return err
			}
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).ExportLedger(ctx, &adminv1alpha1.ExportLedgerRequest{Tenant: project})
			if err != nil {
				return err
			}
			entries := make([]store.LedgerEntry, 0, len(resp.GetEntries()))
			for _, e := range resp.GetEntries() {
				entries = append(entries, store.LedgerEntry{
					ID: e.GetId(), JobID: e.GetJobId(), TaskID: e.GetTaskId(),
					Tenant: e.GetTenant(), Target: e.GetTarget(),
					Unit: e.GetUnit(), Amount: e.GetAmount(),
				})
			}
			return accounting.WriteCSV(cmd.OutOrStdout(), accounting.Normalize(entries, policy))
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project tenant string (empty = all you can see)")
	cmd.Flags().StringVar(&policyPath, "policy", "", "normalization policy YAML (required)")
	_ = cmd.MarkFlagRequired("policy")
	return cmd
}
