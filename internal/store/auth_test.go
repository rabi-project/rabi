// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"errors"
	"testing"

	"github.com/rabi-project/rabi/internal/auth"
	"github.com/rabi-project/rabi/internal/store"
)

func TestTokenLifecycle(t *testing.T) {
	ctx := t.Context()
	plain, id, hash, err := auth.MintToken()
	if err != nil {
		t.Fatal(err)
	}
	rec := &store.TokenRecord{
		ID: id, Name: "ci-bot", Project: "acme", Role: "member",
		TokenHash: hash, CreatedBy: "test-admin",
	}
	if err := testStore.InsertToken(ctx, rec); err != nil {
		t.Fatal(err)
	}

	got, err := testStore.GetToken(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyTokenHash(plain, got.TokenHash) {
		t.Fatal("stored hash does not verify the minted token")
	}
	if got.RevokedAt != nil {
		t.Fatal("fresh token already revoked")
	}
	if got.TokenHash == plain {
		t.Fatal("plaintext stored at rest")
	}

	list, err := testStore.ListTokens(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tok := range list {
		if tok.ID == id {
			found = true
			if tok.TokenHash != "" {
				t.Fatal("ListTokens leaked a hash")
			}
		}
	}
	if !found {
		t.Fatal("token missing from project listing")
	}

	if ok, err := testStore.RevokeToken(ctx, id); err != nil || !ok {
		t.Fatalf("revoke: %v ok=%v", err, ok)
	}
	// Idempotent revoke keeps the original timestamp.
	got1, _ := testStore.GetToken(ctx, id)
	if ok, err := testStore.RevokeToken(ctx, id); err != nil || !ok {
		t.Fatalf("second revoke: %v ok=%v", err, ok)
	}
	got2, _ := testStore.GetToken(ctx, id)
	if got1.RevokedAt == nil || got2.RevokedAt == nil || !got1.RevokedAt.Equal(*got2.RevokedAt) {
		t.Fatal("revoke is not idempotent on the timestamp")
	}

	if _, err := testStore.GetToken(ctx, "nonexistent"); !errors.Is(err, store.ErrTokenNotFound) {
		t.Fatalf("want ErrTokenNotFound, got %v", err)
	}
}

func TestAuditAppend(t *testing.T) {
	ctx := t.Context()
	e := store.AuditEntry{
		PrincipalType: "token", Subject: "abc123", PrincipalName: "ci-bot",
		Role: "viewer", Method: "/tangle.api.v1alpha1.JobsService/SubmitJob",
		Decision: "deny", Reason: "requires role member",
	}
	if err := testStore.RecordAudit(ctx, e); err != nil {
		t.Fatal(err)
	}
	entries, err := testStore.AuditEntries(ctx, "deny", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0] != e {
		t.Fatalf("latest deny entry mismatch: %+v", entries)
	}
}
