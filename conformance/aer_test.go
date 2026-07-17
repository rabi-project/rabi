// SPDX-License-Identifier: Apache-2.0

// T2.conf-aer: the conformance suite against the reference Aer adapter — the
// harness's own control experiment. The adapter process is spawned via uv.
package conformance

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

type tWrap struct{ *testing.T }

func (w tWrap) Run(name string, f func(T)) bool {
	return w.T.Run(name, func(tt *testing.T) { f(tWrap{tt}) })
}

func freePort(t *testing.T) int {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lis.Close() }()
	return lis.Addr().(*net.TCPAddr).Port
}

func TestAerAdapterConformance(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed; adapter conformance runs where Python tooling exists")
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command("uv", "run", "tangle-adapter-aer",
		"--config", "config/single.yaml", "--listen", addr)
	cmd.Dir = "../adapters/aer"
	// uv spawns python as a child: give the tree its own process group so
	// cleanup can kill all of it, and keep its output off the test pipes.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if os.Getenv("TANGLE_CONF_VERBOSE") != "" {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting adapter: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var suite *Suite
	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		suite, err = Dial(ctx, addr, "aer-alpha")
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("adapter never became ready: %v", err)
	}

	suite.Run(ctx, tWrap{t})
}
