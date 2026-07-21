// SPDX-License-Identifier: Apache-2.0

// rabi-shadow reports the shadow-scheduling comparison for a candidate policy
// (phase2-build-plan.md P2.M5): how often it agrees with the active policy, and
// its fidelity-proxy / SLO / wait deltas with confidence intervals, plus a
// promotion verdict. It reads recorded shadow placements; it never schedules.
//
//	rabi-shadow report --db "$RABI_DATABASE_URL" --policy calib-aware/v0 --since 336h
//	rabi-shadow report --db "$RABI_DATABASE_URL" --policy calib-aware/v0 --require-promotable
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rabi-project/rabi/internal/shadow"
	"github.com/rabi-project/rabi/internal/store"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "rabi-shadow",
		Short:         "Shadow-scheduling comparison + promotion verdict",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newReportCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rabi-shadow:", err)
		os.Exit(1)
	}
}

func newReportCmd() *cobra.Command {
	var dsn, policy string
	var since time.Duration
	var floor float64
	var asJSON, requirePromotable bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Compare a candidate policy against the active one from shadow data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("RABI_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("no database: pass --db or set RABI_DATABASE_URL")
			}
			ctx := cmd.Context()
			st, err := store.OpenNoMigrate(ctx, dsn) // read-only; never migrates
			if err != nil {
				return err
			}
			defer st.Close()

			if policy == "" {
				policies, err := st.ShadowPolicies(ctx)
				if err != nil {
					return err
				}
				if len(policies) == 0 {
					return fmt.Errorf("no shadow placements recorded yet")
				}
				policy = policies[0]
			}
			placements, err := st.ShadowPlacementsSince(ctx, policy, time.Now().Add(-since))
			if err != nil {
				return err
			}
			rep := shadow.Analyze(placements, floor)
			promotable, reasons := rep.Promotable()

			if asJSON {
				out := map[string]any{"report": rep, "promotable": promotable, "reasons": reasons}
				blob, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(blob))
			} else {
				printReport(rep, promotable, reasons, since)
			}
			if requirePromotable && !promotable {
				return fmt.Errorf("candidate %s is not promotable", policy)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dsn, "db", "", "database URL (default $RABI_DATABASE_URL)")
	cmd.Flags().StringVar(&policy, "policy", "", "candidate policy (default: first with data)")
	cmd.Flags().DurationVar(&since, "since", 14*24*time.Hour, "window to analyze")
	cmd.Flags().Float64Var(&floor, "floor", shadow.DefaultQualityFloor, "ESP quality floor for the SLO proxy")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	cmd.Flags().BoolVar(&requirePromotable, "require-promotable", false, "exit non-zero if not promotable")
	return cmd
}

func printReport(r shadow.Report, promotable bool, reasons []string, since time.Duration) {
	fmt.Printf("Shadow report: %s vs active %s (window %s)\n", r.Policy, r.ActivePolicy, since)
	fmt.Printf("  samples:        %d\n", r.Samples)
	fmt.Printf("  agreement:      %.1f%%\n", r.AgreementRate*100)
	fmt.Printf("  ESP (fidelity): active %.4f → candidate %.4f\n", r.ActiveESPMean, r.ShadowESPMean)
	fmt.Printf("  ESP delta:      %+.4f  (95%% CI %+.4f .. %+.4f)\n", r.ESPDelta.Mean, r.ESPDelta.CILo, r.ESPDelta.CIHi)
	fmt.Printf("  SLO delta:      %+.4f  (95%% CI %+.4f .. %+.4f)  [floor ESP<%.2f]\n", r.SLODelta.Mean, r.SLODelta.CILo, r.SLODelta.CIHi, r.QualityFloor)
	fmt.Printf("  wait delta (s): %+.2f  (95%% CI %+.2f .. %+.2f)\n", r.WaitDelta.Mean, r.WaitDelta.CILo, r.WaitDelta.CIHi)
	verdict := "NOT promotable"
	if promotable {
		verdict = "PROMOTABLE"
	}
	fmt.Printf("  verdict:        %s\n", verdict)
	for _, r := range reasons {
		fmt.Printf("    - %s\n", r)
	}
}
