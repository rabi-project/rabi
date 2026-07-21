// SPDX-License-Identifier: Apache-2.0

package ha_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rabi-project/rabi/internal/ha"
)

var testDSN string

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	testDSN, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(m.Run())
}

func awaitLeader(t *testing.T, e *ha.Elector, want bool, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if e.IsLeader() == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestFailoverDrill is the P2.M8+ failover drill: two electors campaign for the
// same leadership lock; exactly one leads. When the leader dies (its context is
// cancelled, ending its Postgres session and releasing the advisory lock), the
// standby takes over — and the recovery time (RTO) is measured and bounded.
func TestFailoverDrill(t *testing.T) {
	ctx := context.Background()
	const key = int64(0x11111111)
	const interval = 100 * time.Millisecond

	// Leader A.
	ctxA, cancelA := context.WithCancel(ctx)
	a, err := ha.NewElector(ctxA, testDSN, key, interval, nil)
	if err != nil {
		t.Fatal(err)
	}
	go a.Campaign(ctxA)
	if !awaitLeader(t, a, true, 3*time.Second) {
		t.Fatal("A never became leader")
	}

	// Standby B: must NOT lead while A holds the lock.
	ctxB, cancelB := context.WithCancel(ctx)
	defer cancelB()
	b, err := ha.NewElector(ctxB, testDSN, key, interval, nil)
	if err != nil {
		t.Fatal(err)
	}
	go b.Campaign(ctxB)
	time.Sleep(400 * time.Millisecond)
	if b.IsLeader() {
		t.Fatal("two leaders at once — advisory lock did not enforce single leadership")
	}

	// Failover: kill A. Its session ends, Postgres releases the lock, B takes over.
	t0 := time.Now()
	cancelA()
	if !awaitLeader(t, b, true, 5*time.Second) {
		t.Fatal("standby never took over after leader died")
	}
	rto := time.Since(t0)
	t.Logf("failover RTO = %v (poll interval %v)", rto.Round(time.Millisecond), interval)
	if rto > 3*time.Second {
		t.Errorf("RTO %v exceeded bound", rto)
	}
}

// TestSingleLeader confirms one elector acquires and, once released, a fresh one
// can re-acquire (lock is not stuck).
func TestSingleLeaderAcquireRelease(t *testing.T) {
	ctx := context.Background()
	const key = int64(0x22222222)
	ctx1, cancel1 := context.WithCancel(ctx)
	e1, err := ha.NewElector(ctx1, testDSN, key, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	go e1.Campaign(ctx1)
	if !awaitLeader(t, e1, true, 2*time.Second) {
		t.Fatal("e1 never led")
	}
	cancel1()
	time.Sleep(200 * time.Millisecond)

	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	e2, err := ha.NewElector(ctx2, testDSN, key, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	go e2.Campaign(ctx2)
	if !awaitLeader(t, e2, true, 2*time.Second) {
		t.Fatal("e2 could not acquire after e1 released — lock stuck")
	}
}
