// SPDX-License-Identifier: Apache-2.0

// rabi-load is the Phase 2 load & soak driver (phase2-build-plan.md P2.M2). It
// boots the real control-plane stack against a DEDICATED Postgres, drives a
// storm or soak, checks the test-plan §4 thresholds, and writes a JSON report.
//
//	# storm: 10,000 jobs across 100 synthetic targets (CI weekly)
//	rabi-load storm --db "$LOAD_DATABASE_URL" --jobs 10000 --targets 100 --out storm.json
//
//	# fleet-0-sized variant
//	rabi-load storm --db "$LOAD_DATABASE_URL" --jobs 1000 --targets 20 --out storm-fleet0.json
//
//	# soak: accelerated replay (CI monthly)
//	rabi-load soak --db "$LOAD_DATABASE_URL" --seconds 3600 --out soak.json
//
// The --db MUST be a throwaway load database, never production: the driver runs
// its own dispatcher, so pointing it at fleet-0 would double-schedule. A
// non-zero exit means a threshold was breached — which is what blocks a release
// tag when this runs in the release gate.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rabi-project/rabi/internal/loadtest"
	"github.com/rabi-project/rabi/internal/store"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "rabi-load",
		Short:         "Load & soak driver for the control plane",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newStormCmd(), newSoakCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rabi-load:", err)
		os.Exit(1)
	}
}

func openStore(ctx context.Context, dsn string) (*store.Store, error) {
	if dsn == "" {
		dsn = os.Getenv("LOAD_DATABASE_URL")
	}
	if dsn == "" {
		return nil, fmt.Errorf("no database: pass --db or set LOAD_DATABASE_URL (must be a throwaway load DB, never production)")
	}
	return store.Open(ctx, dsn)
}

// writeReport emits the result as JSON (to --out and stdout) and returns a
// non-nil error when any threshold was breached.
func writeReport(kind string, out string, payload any, violations []string) error {
	report := map[string]any{
		"kind":       kind,
		"result":     payload,
		"violations": violations,
		"pass":       len(violations) == 0,
	}
	blob, _ := json.MarshalIndent(report, "", "  ")
	if out != "" {
		if err := os.WriteFile(out, blob, 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	fmt.Println(string(blob))
	if len(violations) > 0 {
		return fmt.Errorf("%s FAILED: %d threshold breach(es)", kind, len(violations))
	}
	return nil
}

func newStormCmd() *cobra.Command {
	var dsn, out string
	var jobs, targets int
	cmd := &cobra.Command{
		Use:   "storm",
		Short: "Seed a backlog and measure scheduler-cycle + API p99 while it drains",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer st.Close()
			s, err := loadtest.NewStack(cmd.Context(), st, targets, "")
			if err != nil {
				return err
			}
			defer s.Close()
			res, err := loadtest.RunStorm(cmd.Context(), s, loadtest.StormConfig{Jobs: jobs, Targets: targets})
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, res.String())
			return writeReport("storm", out, res, res.Violations())
		},
	}
	cmd.Flags().StringVar(&dsn, "db", "", "database URL (default $LOAD_DATABASE_URL); throwaway DB only")
	cmd.Flags().StringVar(&out, "out", "", "write JSON report to this path")
	cmd.Flags().IntVar(&jobs, "jobs", 10000, "backlog size")
	cmd.Flags().IntVar(&targets, "targets", 100, "synthetic targets")
	return cmd
}

func newSoakCmd() *cobra.Command {
	var dsn, out string
	var seconds, targets, rate int
	cmd := &cobra.Command{
		Use:   "soak",
		Short: "Churn jobs under accelerated replay; measure memory, goroutines, stuck jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openStore(cmd.Context(), dsn)
			if err != nil {
				return err
			}
			defer st.Close()
			s, err := loadtest.NewStack(cmd.Context(), st, targets, "")
			if err != nil {
				return err
			}
			defer s.Close()
			res, err := loadtest.RunSoak(cmd.Context(), s, loadtest.SoakConfig{
				Duration: time.Duration(seconds) * time.Second, Targets: targets, ArrivalRate: rate,
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, res.String())
			return writeReport("soak", out, res, res.Violations())
		},
	}
	cmd.Flags().StringVar(&dsn, "db", "", "database URL (default $LOAD_DATABASE_URL); throwaway DB only")
	cmd.Flags().StringVar(&out, "out", "", "write JSON report to this path")
	cmd.Flags().IntVar(&seconds, "seconds", 3600, "accelerated soak duration in seconds")
	cmd.Flags().IntVar(&targets, "targets", 20, "synthetic targets")
	cmd.Flags().IntVar(&rate, "rate", 200, "job arrival rate per second")
	return cmd
}
