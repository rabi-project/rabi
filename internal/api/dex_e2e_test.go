// SPDX-License-Identifier: Apache-2.0

// M1 acceptance: dex-backed e2e login. A real dex IdP runs in a container;
// the control plane verifies its JWTs via discovery + JWKS (coreos/go-oidc),
// and dex's mock identity (groups: ["authors"]) exercises the group→role
// mapping end to end. The password grant keeps the flow headless — the same
// verification path `qctl login`'s browser flow produces tokens for.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
	"github.com/rabi-project/rabi/internal/api"
	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/job"
	"github.com/rabi-project/rabi/internal/registry"
)

const dexImage = "dexidp/dex:v2.41.1"

// freePort reserves a host port so dex's issuer URL (baked into its config
// before start) matches the port the container is reachable on.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	_ = lis.Close()
	return port
}

func startDex(t *testing.T, ctx context.Context) (issuer string) {
	t.Helper()
	port := freePort(t)
	issuer = fmt.Sprintf("http://127.0.0.1:%d/dex", port)

	cfg := fmt.Sprintf(`
issuer: %s
storage: { type: memory }
web: { http: 0.0.0.0:5556 }
oauth2:
  skipApprovalScreen: true
connectors:
  - type: mockCallback
    id: mock
    name: Mock
staticClients:
  - id: rabi-e2e
    name: rabi e2e
    secret: rabi-e2e-secret
    redirectURIs: ["http://127.0.0.1:5555/callback"]
`, issuer)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        dexImage,
			Cmd:          []string{"dex", "serve", "/etc/dex/cfg/config.yaml"},
			ExposedPorts: []string{"5556/tcp"},
			HostConfigModifier: func(hc *container.HostConfig) {
				// The issuer URL is baked into dex's config before start, so
				// the host port must be pinned, not random.
				hc.PortBindings = network.PortMap{network.MustParsePort("5556/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: fmt.Sprint(port)}}}
			},
			Files: []testcontainers.ContainerFile{{
				HostFilePath: cfgPath, ContainerFilePath: "/etc/dex/cfg/config.yaml", FileMode: 0o644,
			}},
			WaitingFor: wait.ForHTTP("/dex/.well-known/openid-configuration").
				WithPort("5556/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("starting dex: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	return issuer
}

// dexIDToken drives the real authorization-code flow headlessly: dex's
// mockCallback connector approves without a login form (identity "Kilgore
// Trout", groups ["authors"]), so following redirects until the registered
// callback URI yields the code — the same flow `qctl login` runs with a
// browser.
func dexIDToken(t *testing.T, issuer string) string {
	t.Helper()
	var code string
	client := &http.Client{
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Host == "127.0.0.1:5555" {
				code = req.URL.Query().Get("code")
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	authURL := issuer + "/auth?" + url.Values{
		"client_id":     {"rabi-e2e"},
		"redirect_uri":  {"http://127.0.0.1:5555/callback"},
		"response_type": {"code"},
		"scope":         {"openid profile email groups"},
		"state":         {"e2e-state"},
	}.Encode()
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	_ = resp.Body.Close()
	if code == "" {
		t.Fatalf("no authorization code captured (final status %d)", resp.StatusCode)
	}

	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {"http://127.0.0.1:5555/callback"},
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		issuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("rabi-e2e", "rabi-e2e-secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if tokResp.StatusCode != http.StatusOK || body.IDToken == "" {
		t.Fatalf("code exchange failed: status %d, id_token empty=%v", tokResp.StatusCode, body.IDToken == "")
	}
	return body.IDToken
}

func TestDexOIDCLoginE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("dex e2e needs docker")
	}
	ctx := t.Context()
	issuer := startDex(t, ctx)

	verifier, err := auth.NewOIDCVerifier(ctx, auth.OIDCConfig{
		Issuer:      issuer,
		ClientID:    "rabi-e2e",
		GroupRoles:  map[string]auth.Role{"authors": auth.RoleAdmin}, // dex's mock identity carries groups: ["authors"]
		DefaultRole: auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("oidc discovery against dex: %v", err)
	}

	// A dedicated server instance with OIDC enabled and NO bootstrap token:
	// the JWT is the only way in.
	validator, err := job.NewValidator()
	if err != nil {
		t.Fatal(err)
	}
	authn, err := api.NewAuthenticator("", verifier, testStore, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	srv, err := api.New(api.Config{
		GRPCAddr: "127.0.0.1:0", HTTPAddr: "127.0.0.1:0",
		Auth: authn, Registry: reg, Fleet: reg, Store: testStore, Validator: validator,
	})
	if err != nil {
		t.Fatal(err)
	}
	srvCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = srv.Run(srvCtx) }()

	conn, err := grpc.NewClient(srv.GRPCAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	admin := adminv1alpha1.NewAdminServiceClient(conn)

	rawJWT := dexIDToken(t, issuer)
	authed := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+rawJWT)

	who, err := admin.WhoAmI(authed, &adminv1alpha1.WhoAmIRequest{})
	if err != nil {
		t.Fatalf("WhoAmI with dex JWT: %v", err)
	}
	if who.GetPrincipalType() != "oidc" {
		t.Fatalf("principal type = %q, want oidc", who.GetPrincipalType())
	}
	if who.GetRole() != "admin" {
		t.Fatalf("role = %q, want admin via groups=[authors] mapping", who.GetRole())
	}

	// The OIDC admin can run the token lifecycle end to end.
	created, err := admin.CreateToken(authed, &adminv1alpha1.CreateTokenRequest{
		Name: "minted-by-oidc", Project: "dex-e2e", Role: "member",
	})
	if err != nil {
		t.Fatalf("OIDC admin minting token: %v", err)
	}
	if !auth.IsToken(created.GetToken()) {
		t.Fatalf("minted token malformed: %q", created.GetToken())
	}

	// Tampered JWT must fail closed.
	tampered := rawJWT[:len(rawJWT)-2] + "xx"
	badCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+tampered)
	if _, err := admin.WhoAmI(badCtx, &adminv1alpha1.WhoAmIRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("tampered JWT must be Unauthenticated, got %v", err)
	}
}
