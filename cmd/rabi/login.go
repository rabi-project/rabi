// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// rabi login runs the OIDC authorization-code + PKCE flow against any
// spec-compliant IdP, listening on a localhost callback, and stores the
// resulting tokens (0600) for later commands. The bearer presented to rabi
// is the ID token — its audience is the client id the server verifies.
type storedCredentials struct {
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	IDToken      string    `json:"id_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry"`
}

func credentialsPath() (string, error) {
	if p := os.Getenv("RABI_CREDENTIALS"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "rabi", "credentials.json"), nil
}

func newLoginCmd() *cobra.Command {
	var issuer, clientID, listen string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in via OIDC (authorization code + PKCE) and store credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if issuer == "" || clientID == "" {
				return fmt.Errorf("set --issuer/--client-id (or RABI_OIDC_ISSUER/RABI_OIDC_CLIENT_ID)")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			return runLogin(ctx, cmd, issuer, clientID, listen, noBrowser)
		},
	}
	cmd.Flags().StringVar(&issuer, "issuer", os.Getenv("RABI_OIDC_ISSUER"), "OIDC issuer URL")
	cmd.Flags().StringVar(&clientID, "client-id", os.Getenv("RABI_OIDC_CLIENT_ID"), "OIDC client id")
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:0", "localhost callback address")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the login URL instead of opening a browser")
	return cmd
}

func runLogin(ctx context.Context, cmd *cobra.Command, issuer, clientID, listen string, noBrowser bool) error {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return fmt.Errorf("discovering issuer: %w", err)
	}

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("callback listener: %w", err)
	}
	defer func() { _ = lis.Close() }()

	conf := oauth2.Config{
		ClientID:    clientID,
		Endpoint:    provider.Endpoint(),
		RedirectURL: fmt.Sprintf("http://%s/callback", lis.Addr().String()),
		Scopes:      []string{oidc.ScopeOpenID, "profile", "email", "groups", oidc.ScopeOfflineAccess},
	}
	state, err := randomHex(16)
	if err != nil {
		return err
	}
	pkceVerifier := oauth2.GenerateVerifier()
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(pkceVerifier))

	type callback struct {
		code string
		err  error
	}
	got := make(chan callback, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			q := r.URL.Query()
			switch {
			case q.Get("state") != state:
				http.Error(w, "state mismatch", http.StatusBadRequest)
				got <- callback{err: errors.New("state mismatch on callback")}
			case q.Get("error") != "":
				http.Error(w, q.Get("error_description"), http.StatusBadRequest)
				got <- callback{err: fmt.Errorf("idp error: %s", q.Get("error"))}
			default:
				_, _ = fmt.Fprintln(w, "Logged in — you can close this tab and return to rabi.")
				got <- callback{code: q.Get("code")}
			}
		})}
	go func() { _ = srv.Serve(lis) }()
	defer func() { _ = srv.Close() }()

	if noBrowser || !openBrowser(authURL) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Open this URL to log in:\n\n  %s\n\n", authURL)
	}

	var cb callback
	select {
	case cb = <-got:
	case <-ctx.Done():
		return fmt.Errorf("login timed out waiting for the browser callback")
	}
	if cb.err != nil {
		return cb.err
	}

	tok, err := conf.Exchange(ctx, cb.code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return fmt.Errorf("exchanging code: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return errors.New("idp returned no id_token")
	}
	idTok, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(ctx, rawID)
	if err != nil {
		return fmt.Errorf("verifying id_token: %w", err)
	}

	if err := saveCredentials(storedCredentials{
		Issuer: issuer, ClientID: clientID, IDToken: rawID,
		RefreshToken: tok.RefreshToken, Expiry: idTok.Expiry,
	}); err != nil {
		return err
	}
	path, _ := credentialsPath()
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in as %s (credentials: %s)\n", idTok.Subject, path)
	return nil
}

func saveCredentials(c storedCredentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadLoginBearer returns the stored ID token, refreshing it via the refresh
// token when expired. Returns "" (no error) when no credentials are stored.
func loadLoginBearer(ctx context.Context) (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var c storedCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}
	if time.Until(c.Expiry) > time.Minute {
		return c.IDToken, nil
	}
	if c.RefreshToken == "" {
		return "", fmt.Errorf("stored login expired; run `rabi login` again")
	}

	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return "", fmt.Errorf("refreshing login: %w", err)
	}
	conf := oauth2.Config{ClientID: c.ClientID, Endpoint: provider.Endpoint()}
	tok, err := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: c.RefreshToken}).Token()
	if err != nil {
		return "", fmt.Errorf("stored login expired and refresh failed (%v); run `rabi login` again", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return "", errors.New("refresh returned no id_token; run `rabi login` again")
	}
	idTok, err := provider.Verifier(&oidc.Config{ClientID: c.ClientID}).Verify(ctx, rawID)
	if err != nil {
		return "", fmt.Errorf("verifying refreshed id_token: %w", err)
	}
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	c.IDToken, c.Expiry = rawID, idTok.Expiry
	if err := saveCredentials(c); err != nil {
		return "", err
	}
	return rawID, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func openBrowser(url string) bool {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "linux":
		c = exec.Command("xdg-open", url)
	default:
		return false
	}
	return c.Start() == nil
}
