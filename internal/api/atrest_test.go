// SPDX-License-Identifier: Apache-2.0

// M1 acceptance: token plaintext exists only in the mint response — never at
// rest. Rather than grepping source, this scans every text-castable column of
// every user table in the live database for the plaintext. It also enforces
// the "no password storage, ever" constraint at the schema level.
package api_test

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	adminv1alpha1 "github.com/rabi-project/rabi/gen/go/rabi/admin/v1alpha1"
)

func TestNoPlaintextTokenAtRest(t *testing.T) {
	conn, err := grpc.NewClient(testAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+testAPIKey)

	created, err := adminv1alpha1.NewAdminServiceClient(conn).CreateToken(ctx, &adminv1alpha1.CreateTokenRequest{
		Name: "at-rest-probe", Project: "atrest", Role: "viewer",
	})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := created.GetToken()
	secret := plaintext[strings.LastIndex(plaintext, "_")+1:]

	rows, err := testStore.Pool.Query(t.Context(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if len(tables) < 5 {
		t.Fatalf("only %d tables found; schema scan looks broken: %v", len(tables), tables)
	}

	for _, table := range tables {
		var hits int64
		// Cast whole rows to text: catches plaintext hidden in any column,
		// including jsonb.
		q := fmt.Sprintf(`SELECT count(*) FROM %q t WHERE t::text LIKE '%%' || $1 || '%%'`, table)
		if err := testStore.Pool.QueryRow(t.Context(), q, secret).Scan(&hits); err != nil {
			t.Fatalf("scanning %s: %v", table, err)
		}
		if hits > 0 {
			t.Errorf("token secret found at rest in table %q", table)
		}
	}
}

func TestNoPasswordColumnsExist(t *testing.T) {
	var offenders []string
	rows, err := testStore.Pool.Query(t.Context(), `
		SELECT table_name || '.' || column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (column_name ILIKE '%password%' OR column_name ILIKE '%passwd%')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		offenders = append(offenders, c)
	}
	if len(offenders) > 0 {
		t.Fatalf("password columns are forbidden (phase1-build-plan.md §2): %v", offenders)
	}
}
