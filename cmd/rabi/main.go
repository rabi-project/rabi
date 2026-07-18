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

	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/dispatch"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
	"github.com/rabi-project/rabi/internal/store"
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

	dbURL := envOr("RABI_DB_URL", "postgres://rabi:rabi@localhost:5432/rabi?sslmode=disable")
	grpcAddr := envOr("RABI_GRPC_ADDR", ":9090")
	httpAddr := envOr("RABI_HTTP_ADDR", ":8080")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, dbURL)
	if err != nil {
		logger.Error("opening store", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("store ready", "url", dbURL)

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

	logger.Info("rabi serving", "grpc", grpcAddr, "http", httpAddr,
		"adapters", os.Getenv("RABI_ADAPTERS"))
	if err := srv.Run(ctx); err != nil {
		logger.Error("serving", "error", err)
		os.Exit(1)
	}
	logger.Info("rabi stopped")
}
