// SPDX-License-Identifier: Apache-2.0

// Component tests for the load & soak harness against a real Postgres
// (testcontainers). By default they run a fast SMOKE — small job counts and a
// short soak — so they fit the normal coverage pass while still exercising the
// full stack and asserting the real thresholds. The weekly/monthly CI jobs
// scale them up to the test-plan sizes via the RABI_STORM_* / RABI_SOAK_* env
// vars.
package loadtest

import (
	"context"
	"log"
	"os"
	"strconv"
	"testing"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/rabi-project/rabi/internal/store"
)

var testStore *store.Store

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	testStore, err = store.Open(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer testStore.Close()
	os.Exit(m.Run())
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
