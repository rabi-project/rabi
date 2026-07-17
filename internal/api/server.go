// SPDX-License-Identifier: Apache-2.0

// Package api hosts the client-facing surface of tangled: the
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

	apiv1alpha1 "tangle.dev/tangle/gen/go/tangle/api/v1alpha1"
)

// Config carries everything the API server needs.
type Config struct {
	GRPCAddr string // e.g. ":9090"
	HTTPAddr string // e.g. ":8080"
	APIKey   string
	Registry TargetLister
}

// Server runs the gRPC listener and the REST gateway.
type Server struct {
	cfg  Config
	grpc *grpc.Server
	http *http.Server
}

// New assembles the gRPC server and REST gateway. Nothing listens until Run.
func New(cfg Config) (*Server, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("api: APIKey must be set (TANGLE_API_KEY)")
	}
	if cfg.Registry == nil {
		return nil, errors.New("api: Registry must be set")
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryAuthInterceptor(cfg.APIKey)),
		grpc.StreamInterceptor(StreamAuthInterceptor(cfg.APIKey)),
	)
	apiv1alpha1.RegisterJobsServiceServer(grpcServer, &jobsService{})
	apiv1alpha1.RegisterTargetsServiceServer(grpcServer, &targetsService{registry: cfg.Registry})
	apiv1alpha1.RegisterUsageServiceServer(grpcServer, &usageService{})

	return &Server{cfg: cfg, grpc: grpcServer}, nil
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	grpcLis, err := net.Listen("tcp", s.cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("api: listen grpc %s: %w", s.cfg.GRPCAddr, err)
	}

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
	// Liveness for compose healthchecks; deliberately unauthenticated.
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
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
