// SPDX-License-Identifier: Apache-2.0

// qctl is the Tangle CLI: submit, get, watch, cancel, targets, usage.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var (
	flagServer string
	flagToken  string
	flagOutput string
)

// version is stamped at release via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "qctl",
		Short:         "Control a Rabi fleet: submit and track QuantumJobs, inspect the fleet",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagServer, "server",
		envOr("RABI_SERVER", "localhost:9090"), "rabi gRPC address (env RABI_SERVER)")
	root.PersistentFlags().StringVar(&flagToken, "token",
		os.Getenv("RABI_TOKEN"), "bearer credential: API token, OIDC JWT, or bootstrap token (env RABI_TOKEN)")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "output format: table|json")

	root.AddCommand(newTargetsCmd(), newSubmitCmd(), newGetCmd(), newListCmd(),
		newWatchCmd(), newCancelCmd(), newUsageCmd(),
		newTokenCmd(), newWhoAmICmd(), newLoginCmd(),
		newProjectCmd(), newQuotaCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "qctl:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// dial opens the client connection and returns a context carrying auth.
// Credential precedence: --token / RABI_TOKEN, then the credentials saved by
// `qctl login` (refreshed transparently when expired).
func dial(ctx context.Context) (*grpc.ClientConn, context.Context, error) {
	bearer := flagToken
	if bearer == "" {
		var err error
		if bearer, err = loadLoginBearer(ctx); err != nil {
			return nil, nil, err
		}
	}
	if bearer == "" {
		return nil, nil, fmt.Errorf("no credential: set --token / RABI_TOKEN or run `qctl login`")
	}
	conn, err := grpc.NewClient(flagServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", flagServer, err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+bearer)
	return conn, ctx, nil
}

func commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
