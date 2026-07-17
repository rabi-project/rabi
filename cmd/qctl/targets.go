// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
)

func newTargetsCmd() *cobra.Command {
	var modality string
	cmd := &cobra.Command{
		Use:   "targets",
		Short: "List registered fleet targets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()

			resp, err := apiv1alpha1.NewTargetsServiceClient(conn).ListTargets(ctx,
				&apiv1alpha1.ListTargetsRequest{ModalityFilter: modality})
			if err != nil {
				return err
			}

			if flagOutput == "json" {
				// EmitUnpopulated matches the REST gateway's rendering.
				out, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
				if err != nil {
					return err
				}
				fmt.Println(string(out))
				return nil
			}

			targets := resp.GetTargets()
			if len(targets) == 0 {
				fmt.Println("0 targets")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tMODALITY\tQUBITS\tSTATUS")
			for _, t := range targets {
				caps := t.GetCapabilities().AsMap()
				state := t.GetState().AsMap()
				_, _ = fmt.Fprintf(w, "%s\t%v\t%v\t%v\n",
					t.GetName(), lookup(caps, "target", "modality"), lookup(caps, "numQubits"), lookup(state, "status"))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&modality, "modality", "", "filter by modality (e.g. gate-model)")
	return cmd
}

// lookup walks nested string-keyed maps, returning "-" when absent.
func lookup(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "-"
		}
		cur, ok = obj[p]
		if !ok {
			return "-"
		}
	}
	return cur
}
