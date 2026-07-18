// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
)

// newWhoAmICmd verifies the presented credential and shows the principal.
func newWhoAmICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated principal for the current credential",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).WhoAmI(ctx, &adminv1alpha1.WhoAmIRequest{})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "subject:\t%s\nname:\t%s\ntype:\t%s\nrole:\t%s\nproject:\t%s\n",
				resp.GetSubject(), resp.GetName(), resp.GetPrincipalType(), resp.GetRole(), orDash(resp.GetProject()))
			return nil
		},
	}
}

// newTokenCmd groups per-project API-token lifecycle (create/list/revoke).
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage per-project API tokens (admin)",
	}
	cmd.AddCommand(newTokenCreateCmd(), newTokenListCmd(), newTokenRevokeCmd())
	return cmd
}

func newTokenCreateCmd() *cobra.Command {
	var project, role string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Mint a token; the plaintext is printed once and never stored",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).CreateToken(ctx, &adminv1alpha1.CreateTokenRequest{
				Name: args[0], Project: project, Role: role,
			})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"token: %s\nid: %s  project: %s  role: %s\n\nStore the token now — it cannot be shown again.\n",
				resp.GetToken(), resp.GetInfo().GetId(), resp.GetInfo().GetProject(), resp.GetInfo().GetRole())
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project the token is scoped to (required)")
	cmd.Flags().StringVar(&role, "role", "member", "token role: viewer|member|operator|admin")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func newTokenListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List token metadata (never plaintext)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).ListTokens(ctx, &adminv1alpha1.ListTokensRequest{Project: project})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tPROJECT\tROLE\tCREATED\tLAST USED\tSTATUS")
			for _, t := range resp.GetTokens() {
				status := "active"
				if t.GetRevokedAt() != nil {
					status = "revoked " + t.GetRevokedAt().AsTime().Format(time.DateOnly)
				}
				lastUsed := "never"
				if t.GetLastUsedAt() != nil {
					lastUsed = t.GetLastUsedAt().AsTime().Format(time.DateTime)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					t.GetId(), t.GetName(), t.GetProject(), t.GetRole(),
					t.GetCreatedAt().AsTime().Format(time.DateOnly), lastUsed, status)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project (empty = all)")
	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a token immediately (rotation = create + revoke)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := adminv1alpha1.NewAdminServiceClient(conn).RevokeToken(ctx, &adminv1alpha1.RevokeTokenRequest{Id: args[0]})
			if err != nil {
				return err
			}
			if !resp.GetFound() {
				return fmt.Errorf("token %q not found", args[0])
			}
			fmt.Fprintf(cmd.OutOrStdout(), "token %s revoked\n", args[0])
			return nil
		},
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
