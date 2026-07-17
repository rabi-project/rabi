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
	flagAPIKey string
	flagOutput string
)

func main() {
	root := &cobra.Command{
		Use:           "qctl",
		Short:         "Control Tangle: submit and track QuantumJobs, inspect the fleet",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagServer, "server",
		envOr("TANGLE_SERVER", "localhost:9090"), "tangled gRPC address (env TANGLE_SERVER)")
	root.PersistentFlags().StringVar(&flagAPIKey, "api-key",
		os.Getenv("TANGLE_API_KEY"), "API key (env TANGLE_API_KEY)")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "output format: table|json")

	root.AddCommand(newTargetsCmd(), newSubmitCmd(), newGetCmd(), newListCmd(),
		newWatchCmd(), newCancelCmd(), newUsageCmd())

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
func dial(ctx context.Context) (*grpc.ClientConn, context.Context, error) {
	if flagAPIKey == "" {
		return nil, nil, fmt.Errorf("no API key: set --api-key or TANGLE_API_KEY")
	}
	conn, err := grpc.NewClient(flagServer, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", flagServer, err)
	}
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+flagAPIKey)
	return conn, ctx, nil
}

func commandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
