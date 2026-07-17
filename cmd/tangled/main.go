// SPDX-License-Identifier: Apache-2.0

// tangled is the single control-plane binary: API server, scheduler, target
// registry, and accounting (mvp-build-plan.md §2 — no other services).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tangle.dev/tangle/internal/api"
	"tangle.dev/tangle/internal/job"
	"tangle.dev/tangle/internal/registry"
	"tangle.dev/tangle/internal/store"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	apiKey := os.Getenv("TANGLE_API_KEY")
	if apiKey == "" {
		logger.Error("TANGLE_API_KEY must be set")
		os.Exit(1)
	}
	dbURL := envOr("TANGLE_DB_URL", "postgres://tangle:tangle@localhost:5432/tangle?sslmode=disable")
	grpcAddr := envOr("TANGLE_GRPC_ADDR", ":9090")
	httpAddr := envOr("TANGLE_HTTP_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		logger.Error("opening store", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("store ready", "url", dbURL)

	reg := registry.New()

	validator, err := job.NewValidator()
	if err != nil {
		logger.Error("compiling admission schema", "error", err)
		os.Exit(1)
	}

	srv, err := api.New(api.Config{
		GRPCAddr:  grpcAddr,
		HTTPAddr:  httpAddr,
		APIKey:    apiKey,
		Registry:  reg,
		Fleet:     reg,
		Store:     st,
		Validator: validator,
	})
	if err != nil {
		logger.Error("assembling api server", "error", err)
		os.Exit(1)
	}

	logger.Info("tangled serving", "grpc", grpcAddr, "http", httpAddr)
	if err := srv.Run(ctx); err != nil {
		logger.Error("serving", "error", err)
		os.Exit(1)
	}
	logger.Info("tangled stopped")
}
