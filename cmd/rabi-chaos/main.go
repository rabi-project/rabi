// SPDX-License-Identifier: Apache-2.0

// rabi-chaos is the game-day driver for the Phase 2 chaos & invariants harness
// (phase2-build-plan.md P2.M1). The eight fault scenarios live in the CI
// component suite (internal/chaos); this binary is the supervised, annotated
// path for exercising invariants against a live deployment.
//
//	# read-only invariant sweep over a live control plane (safe; the default)
//	rabi-chaos sweep --db "$RABI_DATABASE_URL" --operator edward
//
//	# same, against production fleet-0 — requires explicit confirmation
//	rabi-chaos sweep --db "$RABI_DATABASE_URL" --target fleet0 --i-mean-it \
//	    --operator edward --note "monthly drill"
//
// Every run records a game_days row; the M7 status page renders the latest as
// "last game-day date and result". A sweep never injects a fault or mutates a
// job — it verifies that the five invariants hold on whatever the system's
// current state is, which is the honest first game-day for a production site.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rabi-project/rabi/internal/chaos"
	"github.com/rabi-project/rabi/internal/store"
)

var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "rabi-chaos",
		Short:         "Game-day driver for the chaos & invariants harness",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newSweepCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rabi-chaos:", err)
		os.Exit(1)
	}
}

func newSweepCmd() *cobra.Command {
	var dsn, target, operator, note string
	var iMeanIt bool
	var limit int
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Read-only invariant sweep over a live control plane; records a game-day",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				dsn = os.Getenv("RABI_DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("no database: pass --db or set RABI_DATABASE_URL")
			}
			// Fleet-0 is production. Touching it — even read-only — is a scheduled,
			// confirmed act, never a casual one.
			if target == "fleet0" && !iMeanIt {
				return fmt.Errorf("target fleet0 is production: re-run with --i-mean-it to confirm the drill")
			}
			return runSweep(cmd.Context(), dsn, target, operator, note, limit)
		},
	}
	cmd.Flags().StringVar(&dsn, "db", "", "database URL (default $RABI_DATABASE_URL)")
	cmd.Flags().StringVar(&target, "target", "compose", "deployment under drill: compose | fleet0")
	cmd.Flags().StringVar(&operator, "operator", "", "operator running the drill (recorded)")
	cmd.Flags().StringVar(&note, "note", "", "free-text note for the game-day record")
	cmd.Flags().BoolVar(&iMeanIt, "i-mean-it", false, "required confirmation for --target fleet0")
	cmd.Flags().IntVar(&limit, "limit", 5000, "max recent jobs to sweep")
	return cmd
}

func runSweep(ctx context.Context, dsn, target, operator, note string, limit int) error {
	started := time.Now()
	// Never run migrations against production ad hoc — the upgrade path owns
	// fleet-0's schema. Compose/CI may self-migrate for convenience.
	var st *store.Store
	var err error
	if target == "fleet0" {
		st, err = store.OpenNoMigrate(ctx, dsn)
	} else {
		st, err = store.Open(ctx, dsn)
	}
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	jobs, err := st.ListJobs(ctx, "", "", limit, 0)
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}
	accepted := make([]string, 0, len(jobs))
	declared := make(map[string]float64, len(jobs))
	for _, j := range jobs {
		accepted = append(accepted, j.JobID)
		if s := declaredShots(j); s > 0 {
			declared[j.JobID] = s
		}
	}

	fmt.Printf("rabi-chaos sweep · target=%s · jobs=%d\n", target, len(accepted))
	violations := chaos.CheckAll(ctx, st, accepted, declared)
	green := len(violations) == 0
	for _, v := range violations {
		fmt.Printf("  VIOLATION %s\n", v)
	}

	// Record the drill (append-only). A missing game_days table on an
	// un-migrated production DB is surfaced, not swallowed.
	rec := store.GameDay{
		StartedAt: started, FinishedAt: time.Now(), Scenario: "invariant-sweep",
		Target: target, InvariantsGreen: green, Violations: len(violations),
		Operator: operator, Note: note,
	}
	if rerr := st.RecordGameDay(ctx, rec); rerr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record game-day: %v\n", rerr)
	}

	if !green {
		return fmt.Errorf("invariants RED: %d violation(s)", len(violations))
	}
	fmt.Printf("invariants GREEN across %d jobs (%.1fs)\n", len(accepted), time.Since(started).Seconds())
	return nil
}

// declaredShots pulls spec.workload.gateModel.shots out of a job document for
// the usage-cap invariant; 0 when the job carries no gate-model shot count.
func declaredShots(j *store.JobRecord) float64 {
	spec, _ := j.Doc["spec"].(map[string]any)
	wl, _ := spec["workload"].(map[string]any)
	gm, _ := wl["gateModel"].(map[string]any)
	if s, ok := gm["shots"].(float64); ok {
		return s
	}
	return 0
}
