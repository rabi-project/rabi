// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	apiv1alpha1 "github.com/mAengo31/rabi/gen/go/tangle/api/v1alpha1"
)

func newUsageCmd() *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:   "usage --tenant TENANT",
		Short: "Show native-unit usage per target (no pricing — native units only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := apiv1alpha1.NewUsageServiceClient(conn).GetTenantUsage(ctx,
				&apiv1alpha1.TenantUsageRequest{Tenant: tenant})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			if len(resp.GetUsage()) == 0 {
				fmt.Println("no usage recorded")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "TARGET\tUNIT\tAMOUNT")
			for _, u := range resp.GetUsage() {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%g\n", u.GetTarget(), u.GetUnit(), u.GetAmount())
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant to report")
	_ = cmd.MarkFlagRequired("tenant")
	return cmd
}
