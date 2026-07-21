// SPDX-License-Identifier: Apache-2.0

// Package api hosts the client-facing surface of rabi: the
// tangle.api.v1alpha1 gRPC services and the REST gateway mapped by
// api-config.yaml. There is exactly one control-plane binary
// (mvp-build-plan.md §2); this package is its front door.
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	apiv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/api/v1alpha1"
	"github.com/rabi-project/rabi/internal/console"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/store"
)

// Config carries everything the API server needs.
type Config struct {
	GRPCAddr  string // e.g. ":9090"
	HTTPAddr  string // e.g. ":8080"
	Auth      *Authenticator
	Registry  TargetLister
	Fleet     FleetViewer
	Store     *store.Store
	Validator *job.Validator
	// Canceller, when set, is invoked before a CancelJob transition so
	// in-flight adapter tasks get a best-effort cancel. Optional.
	Canceller TaskCanceller
}

// Server runs the gRPC listener and the REST gateway.
type Server struct {
	cfg     Config
	grpc    *grpc.Server
	http    *http.Server
	grpcLis net.Listener
	now     func() time.Time
	started time.Time
}

// New assembles the gRPC server and REST gateway and binds the gRPC listener
// (so tests may use ":0" and read GRPCAddr). Nothing serves until Run.
func New(cfg Config) (*Server, error) {
	if cfg.Auth == nil {
		return nil, errors.New("api: Auth must be set")
	}
	if cfg.Registry == nil {
		return nil, errors.New("api: Registry must be set")
	}
	if cfg.Fleet == nil {
		return nil, errors.New("api: Fleet must be set")
	}
	if cfg.Store == nil {
		return nil, errors.New("api: Store must be set")
	}
	if cfg.Validator == nil {
		return nil, errors.New("api: Validator must be set")
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(cfg.Auth.UnaryAuthInterceptor()),
		grpc.StreamInterceptor(cfg.Auth.StreamAuthInterceptor()),
	)
	apiv1alpha1.RegisterJobsServiceServer(grpcServer, &jobsService{
		store: cfg.Store, validator: cfg.Validator, fleet: cfg.Fleet, canceller: cfg.Canceller,
	})
	apiv1alpha1.RegisterTargetsServiceServer(grpcServer, &targetsService{registry: cfg.Registry})
	apiv1alpha1.RegisterUsageServiceServer(grpcServer, &usageService{store: cfg.Store})
	adminv1alpha1.RegisterAdminServiceServer(grpcServer, &adminService{store: cfg.Store})

	grpcLis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return nil, fmt.Errorf("api: listen grpc %s: %w", cfg.GRPCAddr, err)
	}
	return &Server{cfg: cfg, grpc: grpcServer, grpcLis: grpcLis, now: time.Now, started: time.Now()}, nil
}

// GRPCAddr is the bound gRPC address (useful when configured with ":0").
func (s *Server) GRPCAddr() string { return s.grpcLis.Addr().String() }

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	grpcLis := s.grpcLis

	// The gateway dials our own gRPC listener so streaming endpoints work
	// identically over REST (in-process gateway registration cannot stream).
	gwConn, err := grpc.NewClient(grpcLis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("api: dial gateway loopback: %w", err)
	}
	defer func() { _ = gwConn.Close() }()

	gwMux := runtime.NewServeMux()
	for _, register := range []func(context.Context, *runtime.ServeMux, *grpc.ClientConn) error{
		apiv1alpha1.RegisterJobsServiceHandler,
		apiv1alpha1.RegisterTargetsServiceHandler,
		apiv1alpha1.RegisterUsageServiceHandler,
	} {
		if err := register(ctx, gwMux, gwConn); err != nil {
			return fmt.Errorf("api: register gateway handler: %w", err)
		}
	}

	httpMux := http.NewServeMux()
	// Read-only console (M11): static SPA from the single binary; it calls
	// the REST API below with the viewer's own token.
	httpMux.Handle("/console/", console.Handler())
	httpMux.Handle("/console", http.RedirectHandler("/console/", http.StatusMovedPermanently))
	// Prometheus aggregates (M12); tenant-blind, scraper-open like /healthz.
	httpMux.HandleFunc("/metrics", s.metricsHandler())
	// Public status page (P2.M7): static HTML rendered from the database,
	// aggregate-only, unauthenticated like /healthz and /metrics.
	httpMux.HandleFunc("/status", s.statusHandler())
	// Liveness for compose healthchecks; deliberately unauthenticated.
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	// Readiness (P2.M8+): serve traffic only when the datastore is reachable.
	// Liveness (/healthz) says "the process is up"; readiness says "it can work".
	httpMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.cfg.Store.Pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready: database unreachable\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	httpMux.Handle("/", gwMux)
	s.http = &http.Server{Addr: s.cfg.HTTPAddr, Handler: httpMux, ReadHeaderTimeout: 10 * time.Second}

	errCh := make(chan error, 2)
	go func() {
		if err := s.grpc.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("api: grpc serve: %w", err)
		}
	}()
	go func() {
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("api: http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		s.grpc.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}
