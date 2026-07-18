// SPDX-License-Identifier: Apache-2.0

// rabi-conformance certifies any Tangle adapter against the spec suite and
// emits a signed report (phase1-build-plan.md M7):
//
//	rabi-conformance run --target localhost:50051 [--target-id t1] \
//	    [--out reports/] [--key ed25519.key] [--note "fake-backend mode"]
//
// Category selection follows declared capabilities; declaring a capability
// obligates passing its tests.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rabi-project/rabi/conformance"
	adapterv1alpha1 "github.com/rabi-project/rabi/gen/go/tangle/adapter/v1alpha1"
)

// version is stamped via -ldflags "-X main.version=v0.3.0"; "dev" otherwise.
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:           "rabi-conformance",
		Short:         "Certify a Tangle adapter against the spec conformance suite",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newRunCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rabi-conformance:", err)
		os.Exit(1)
	}
}

func newRunCmd() *cobra.Command {
	var target, targetID, outDir, keyPath string
	var notes []string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the suite against a live adapter and write the signed report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()

			if targetID == "" {
				id, err := firstTargetID(ctx, target)
				if err != nil {
					return err
				}
				targetID = id
			}
			suite, err := conformance.Dial(ctx, target, targetID)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "certifying %s target %q (harness %s, spec %s)\n",
				target, targetID, version, conformance.SpecVersion)
			rec := &conformance.Recorder{}
			suite.Run(ctx, rec)

			report := conformance.BuildReport(version, target, suite, rec, time.Now(), notes)
			key, generated, err := loadOrGenerateKey(keyPath)
			if err != nil {
				return err
			}
			if err := writeArtifacts(outDir, report, key, generated); err != nil {
				return err
			}

			for _, c := range report.Categories {
				res := "pass"
				if !c.Passed {
					res = "FAIL"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %-28s %s\n", c.Name, res)
			}
			if !report.Passed {
				return fmt.Errorf("conformance FAILED (report in %s)", outDir)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "PASSED (report in %s)\n", outDir)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "adapter gRPC address (required)")
	cmd.Flags().StringVar(&targetID, "target-id", "", "target id (default: first listed)")
	cmd.Flags().StringVar(&outDir, "out", "conformance-report", "output directory")
	cmd.Flags().StringVar(&keyPath, "key", "", "ed25519 private key (PEM); generated if omitted")
	cmd.Flags().StringArrayVar(&notes, "note", nil, "context note recorded in the report (repeatable)")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func firstTargetID(ctx context.Context, addr string) (string, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	resp, err := adapterv1alpha1.NewAdapterServiceClient(conn).
		ListTargets(ctx, &adapterv1alpha1.ListTargetsRequest{})
	if err != nil {
		return "", fmt.Errorf("ListTargets: %w", err)
	}
	if len(resp.GetTargets()) == 0 {
		return "", fmt.Errorf("adapter lists no targets")
	}
	return resp.GetTargets()[0].GetTargetId(), nil
}

// loadOrGenerateKey returns the signing key; generated=true means an
// ephemeral key was created (its public half gets written next to the
// report so the signature is still verifiable).
func loadOrGenerateKey(path string) (ed25519.PrivateKey, bool, error) {
	if path == "" {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		return key, true, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, false, fmt.Errorf("%s: not PEM", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, false, fmt.Errorf("%s: not an ed25519 key", path)
	}
	return key, false, nil
}

func writeArtifacts(dir string, report *conformance.Report, key ed25519.PrivateKey, generatedKey bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	doc, err := report.CanonicalJSON()
	if err != nil {
		return err
	}
	sig, err := report.Sign(key)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"report.json": doc,
		"report.md":   []byte(report.Markdown()),
		"report.sig":  sig,
	}
	if generatedKey {
		pub, err := x509.MarshalPKIXPublicKey(key.Public())
		if err != nil {
			return err
		}
		files["report.pub"] = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub})
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
