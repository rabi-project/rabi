// SPDX-License-Identifier: Apache-2.0

// rabi is the single control-plane binary: API server, scheduler, target
// registry, and accounting (mvp-build-plan.md §2 — no other services).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/probe"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
)

// runReconciliation runs the accounting audit on a schedule (weekly in
// production; RABI_RECONCILE_EVERY overrides for demos/tests) inside the
// single binary — no new infrastructure (phase1-build-plan.md §2).
func runReconciliation(ctx context.Context, st *store.Store, logger *slog.Logger) {
	every := 168 * time.Hour
	if v := os.Getenv("RABI_RECONCILE_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			logger.Error("bad RABI_RECONCILE_EVERY; using weekly", "value", v, "error", err)
		} else {
			every = d
		}
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		checked, mismatches, err := st.ReconcileUsage(ctx)
		switch {
		case err != nil:
			logger.Error("reconciliation failed", "error", err)
		case len(mismatches) > 0:
			logger.Error("reconciliation found mismatches — investigate before billing",
				"checked", checked, "mismatches", len(mismatches))
		default:
			logger.Info("reconciliation clean", "checked", checked)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	dbURL := envOr("RABI_DB_URL", "postgres://rabi:rabi@localhost:5432/rabi?sslmode=disable")
	grpcAddr := envOr("RABI_GRPC_ADDR", ":9090")
	httpAddr := envOr("RABI_HTTP_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openStore := store.Open
	if os.Getenv("RABI_AUTO_MIGRATE") == "false" {
		openStore = store.OpenNoMigrate
	}
	st, err := openStore(ctx, dbURL)
	if err != nil {
		logger.Error("opening store", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("store ready", "url", store.RedactDSN(dbURL))

	reg, err := registry.NewFromSpec(os.Getenv("RABI_ADAPTERS"))
	if err != nil {
		logger.Error("configuring adapters", "error", err)
		os.Exit(1)
	}
	reg.Start(ctx)

	dispatcher, err := dispatch.New(st, reg, os.Getenv("RABI_POLICY"), logger)
	if err != nil {
		logger.Error("configuring scheduling policy", "error", err)
		os.Exit(1)
	}
	go dispatcher.Run(ctx)

	validator, err := job.NewValidator()
	if err != nil {
		logger.Error("compiling admission schema", "error", err)
		os.Exit(1)
	}

	authn, err := buildAuthenticator(ctx, st, logger)
	if err != nil {
		logger.Error("configuring auth", "error", err)
		os.Exit(1)
	}

	srv, err := api.New(api.Config{
		GRPCAddr:  grpcAddr,
		HTTPAddr:  httpAddr,
		Auth:      authn,
		Registry:  reg,
		Fleet:     reg,
		Store:     st,
		Validator: validator,
		Canceller: dispatcher,
	})
	if err != nil {
		logger.Error("assembling api server", "error", err)
		os.Exit(1)
	}

	go runReconciliation(ctx, st, logger)

	probeEvery := 15 * time.Minute
	if v := os.Getenv("RABI_PROBE_EVERY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			probeEvery = d
		} else if v == "off" {
			probeEvery = 0
		} else {
			logger.Error("bad RABI_PROBE_EVERY; using 15m", "value", v, "error", err)
		}
	}
	go probe.New(st, reg, probeEvery, logger).Run(ctx)

	logger.Info("rabi serving", "grpc", grpcAddr, "http", httpAddr,
		"adapters", os.Getenv("RABI_ADAPTERS"))
	if err := srv.Run(ctx); err != nil {
		logger.Error("serving", "error", err)
		os.Exit(1)
	}
	logger.Info("rabi stopped")
}
