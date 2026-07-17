// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/yaml"

	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
)

func printProto(m proto.Message) error {
	if flagOutput == "json" {
		out, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}
	out, err := protojson.Marshal(m)
	if err != nil {
		return err
	}
	asYAML, err := yaml.JSONToYAML(out)
	if err != nil {
		return err
	}
	fmt.Print(string(asYAML))
	return nil
}

func jobSummaryLine(w io.Writer, j *apiv1alpha1.Job) {
	st := j.GetStatus().AsMap()
	phase, _ := st["phase"].(string)
	if phase == "" {
		phase = "-"
	}
	doc := j.GetQuantumJob().AsMap()
	name := lookup(doc, "metadata", "name")
	_, _ = fmt.Fprintf(w, "%s\t%v\t%s\t%s\n", j.GetJobId(), name, j.GetTenant(), phase)
}

func newSubmitCmd() *cobra.Command {
	var file string
	var dryRun bool
	var tenant string
	cmd := &cobra.Command{
		Use:   "submit -f job.yaml",
		Short: "Submit a QuantumJob document (YAML or JSON)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var raw []byte
			var err error
			if file == "-" {
				raw, err = io.ReadAll(os.Stdin)
			} else {
				raw, err = os.ReadFile(file)
			}
			if err != nil {
				return err
			}
			asJSON, err := yaml.YAMLToJSON(raw)
			if err != nil {
				return fmt.Errorf("parsing %s: %w", file, err)
			}
			doc := &structpb.Struct{}
			if err := protojson.Unmarshal(asJSON, doc); err != nil {
				return fmt.Errorf("parsing %s: %w", file, err)
			}

			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()

			jobResp, err := apiv1alpha1.NewJobsServiceClient(conn).SubmitJob(ctx, &apiv1alpha1.SubmitJobRequest{
				Tenant:     tenant,
				QuantumJob: doc,
				DryRun:     dryRun,
			})
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Fprintln(os.Stderr, "dry run: validation passed, nothing enqueued")
				return printProto(jobResp)
			}
			st := jobResp.GetStatus().AsMap()
			fmt.Printf("%s\t%v\n", jobResp.GetJobId(), st["phase"])
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "filename", "f", "", "QuantumJob document ('-' for stdin)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate only; do not enqueue")
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant override; must match metadata.tenant when set")
	_ = cmd.MarkFlagRequired("filename")
	return cmd
}

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get JOB_ID",
		Short: "Fetch one job (full document and status)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			jobResp, err := apiv1alpha1.NewJobsServiceClient(conn).GetJob(ctx, &apiv1alpha1.JobRef{JobId: args[0]})
			if err != nil {
				return err
			}
			return printProto(jobResp)
		},
	}
}

func newListCmd() *cobra.Command {
	var tenant, phase string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List jobs (newest first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			resp, err := apiv1alpha1.NewJobsServiceClient(conn).ListJobs(ctx, &apiv1alpha1.ListJobsRequest{
				Tenant: tenant, PhaseFilter: phase,
			})
			if err != nil {
				return err
			}
			if flagOutput == "json" {
				return printProto(resp)
			}
			if len(resp.GetJobs()) == 0 {
				fmt.Println("0 jobs")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "JOB_ID\tNAME\tTENANT\tPHASE")
			for _, j := range resp.GetJobs() {
				jobSummaryLine(w, j)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "filter by tenant")
	cmd.Flags().StringVar(&phase, "phase", "", "filter by phase (PENDING, RUNNING, ...)")
	return cmd
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch JOB_ID",
		Short: "Stream a job's phase transitions until it is terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Watching has no client-side deadline: jobs may run long.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			stream, err := apiv1alpha1.NewJobsServiceClient(conn).WatchJob(ctx, &apiv1alpha1.JobRef{JobId: args[0]})
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "JOB_ID\tNAME\tTENANT\tPHASE")
			_ = w.Flush()
			for {
				jobResp, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				jobSummaryLine(w, jobResp)
				_ = w.Flush()
			}
		},
	}
}

func newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel JOB_ID",
		Short: "Cancel a job (legal from any non-terminal phase)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := commandContext()
			defer cancel()
			conn, ctx, err := dial(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			jobResp, err := apiv1alpha1.NewJobsServiceClient(conn).CancelJob(ctx, &apiv1alpha1.JobRef{JobId: args[0]})
			if err != nil {
				return err
			}
			st := jobResp.GetStatus().AsMap()
			fmt.Printf("%s\t%v\n", jobResp.GetJobId(), st["phase"])
			return nil
		},
	}
}
