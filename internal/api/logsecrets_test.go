// SPDX-License-Identifier: Apache-2.0

package api_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/rabi-project/rabi/internal/api"
)

// scanSecrets returns any sentinel that appears in logs — the log-secret
// scanner. It must have teeth (catch a planted secret) and stay quiet on clean
// logs (P2.M4).
func scanSecrets(logs string, secrets []string) []string {
	var found []string
	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			found = append(found, s)
		}
	}
	return found
}

// TestNoSecretsInLogs proves credentials never reach the log stream. It drives
// the authenticator — the component that handles the bootstrap token and every
// presented bearer — with sentinel values and asserts none appear in captured
// logs, then proves the scanner itself has teeth.
func TestNoSecretsInLogs(t *testing.T) {
	const (
		sentinelBootstrap = "SENTINEL-bootstrap-9d3f2a"
		sentinelBearer    = "SENTINEL-bearer-cred-7a1c8b"
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	authn, err := api.NewAuthenticator(sentinelBootstrap, nil, testStore, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	// A hostile/incorrect bearer must be rejected — and never echoed to the log.
	md := metadata.Pairs("authorization", "Bearer "+sentinelBearer)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	if _, err := authn.Authenticate(ctx); err == nil {
		t.Fatal("sentinel bearer should not authenticate")
	}
	// A correct bootstrap token authenticates — its value still must not log.
	okCtx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+sentinelBootstrap))
	if _, err := authn.Authenticate(okCtx); err != nil {
		t.Fatalf("bootstrap token should authenticate: %v", err)
	}

	if leaked := scanSecrets(buf.String(), []string{sentinelBootstrap, sentinelBearer}); len(leaked) > 0 {
		t.Errorf("SECRET LEAKED INTO LOGS: %v\n--- logs ---\n%s", leaked, buf.String())
	}

	// Teeth: the scanner must catch a secret that is actually present, or the
	// clean result above proves nothing.
	logger.Info("planted", "token", sentinelBootstrap)
	if leaked := scanSecrets(buf.String(), []string{sentinelBootstrap}); len(leaked) == 0 {
		t.Error("scanner failed to catch a planted secret — it has no teeth")
	}
}
