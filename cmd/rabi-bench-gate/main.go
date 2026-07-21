// SPDX-License-Identifier: Apache-2.0

// rabi-bench-gate is the benchmark-as-regression release gate (P2.M5). It
// compares the benchmark's headline metrics for the value-carrying policy
// against a pinned baseline and fails when any regresses more than the
// threshold — unless an RFC-referenced justification is supplied, which is how a
// deliberate trade-off ships (the baseline then moves in a policy-promotion PR).
//
//	rabi-bench-gate --baseline bench/baseline.json --current bench/results/summary.csv
//	rabi-bench-gate --baseline bench/baseline.json --current new.csv --rfc RFC-0012
package main

import (
	"fmt"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/rabi-project/rabi/internal/benchgate"
)

var version = "dev"

var rfcPattern = regexp.MustCompile(`^RFC-\d+`)

func main() {
	var baselinePath, currentPath, rfc string
	var threshold float64
	cmd := &cobra.Command{
		Use:           "rabi-bench-gate",
		Short:         "Block a release on a benchmark headline-metric regression",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			baseline, err := benchgate.LoadBaselineJSON(baselinePath)
			if err != nil {
				return err
			}
			current, err := benchgate.LoadSummaryCSV(currentPath, baseline.Policy)
			if err != nil {
				return err
			}
			regs := benchgate.Check(baseline, current, threshold)
			fmt.Printf("bench gate: policy %s, threshold %.0f%%\n", baseline.Policy, threshold*100)
			if len(regs) == 0 {
				fmt.Println("  no headline-metric regression — OK")
				return nil
			}
			for _, r := range regs {
				fmt.Printf("  REGRESSION: %s\n", r)
			}
			if rfc != "" && rfcPattern.MatchString(rfc) {
				fmt.Printf("  ALLOWED by justification %s — remember to move bench/baseline.json in a policy-promotion PR\n", rfc)
				return nil
			}
			return fmt.Errorf("%d headline-metric regression(s) block this release; supply --rfc RFC-XXXX to justify a deliberate trade-off", len(regs))
		},
	}
	cmd.Flags().StringVar(&baselinePath, "baseline", "bench/baseline.json", "pinned baseline metrics JSON")
	cmd.Flags().StringVar(&currentPath, "current", "bench/results/summary.csv", "current benchmark summary CSV")
	cmd.Flags().Float64Var(&threshold, "threshold", 0.05, "regression threshold (fraction)")
	cmd.Flags().StringVar(&rfc, "rfc", "", "RFC reference justifying a deliberate regression")
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rabi-bench-gate:", err)
		os.Exit(1)
	}
}
