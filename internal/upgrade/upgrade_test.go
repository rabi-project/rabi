// SPDX-License-Identifier: Apache-2.0

// Package upgrade's tests are the Phase 2 upgrade & migration hardening suite
// (P2.M3): a forward-migration matrix over golden databases captured from every
// released tag, an upgrade rehearsal (roll the control plane under live load,
// in-flight jobs survive, bounded API unavailability), and a rollback-safety
// check (the schema stays additive, so the N-1 binary runs against the N
// schema). Each test gets its own fresh database inside one shared Postgres
// container.
package upgrade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var baseDSN string

func TestMain(m *testing.M) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:15-alpine",
		tcpostgres.WithDatabase("rabi"), tcpostgres.WithUsername("rabi"),
		tcpostgres.WithPassword("rabi"), tcpostgres.BasicWaitStrategies())
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer func() { _ = pg.Terminate(ctx) }()
	baseDSN, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(m.Run())
}

var dbCounter atomic.Int64

// freshDB creates a uniquely-named empty database in the shared container and
// returns a DSN pointing at it. Each test migrates its own database in
// isolation, so a golden restore in one test cannot affect another.
func freshDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("g%d", dbCounter.Add(1))
	admin, err := pgx.Connect(t.Context(), baseDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// execSeed runs a golden seed SQL file against dsn as the owner.
func execSeed(t *testing.T, dsn, sqlPath string) {
	t.Helper()
	blob, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("seed connect: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(t.Context(), string(blob)); err != nil {
		t.Fatalf("apply seed %s: %v", filepath.Base(sqlPath), err)
	}
}

type golden struct {
	Tag     string `json:"tag"`
	Version int64  `json:"version"`
	Seed    string `json:"seed"`
}

func loadManifest(t *testing.T) []golden {
	t.Helper()
	blob, err := os.ReadFile(filepath.Join("testdata", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var doc struct {
		Goldens []golden `json:"goldens"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(doc.Goldens) == 0 {
		t.Fatal("manifest has no goldens")
	}
	return doc.Goldens
}
