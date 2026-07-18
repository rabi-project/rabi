// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// API tokens look like "rabi_<id>_<secret>": the id is stored in the clear
// for O(1) lookup, the full token is stored only as a SHA-256 hex digest
// (hard constraint: hashed at rest, phase1-build-plan.md §2). The secret is
// 32 random bytes, so the unsalted digest of a high-entropy secret is safe
// and deterministic for lookup.
const tokenPrefix = "rabi_"

// MintToken returns a new plaintext token (shown exactly once) with its id
// and storage hash.
func MintToken() (plaintext, id, hash string, err error) {
	idb := make([]byte, 6)
	sec := make([]byte, 32)
	if _, err := rand.Read(idb); err != nil {
		return "", "", "", fmt.Errorf("mint token id: %w", err)
	}
	if _, err := rand.Read(sec); err != nil {
		return "", "", "", fmt.Errorf("mint token secret: %w", err)
	}
	id = hex.EncodeToString(idb)
	plaintext = tokenPrefix + id + "_" + hex.EncodeToString(sec)
	return plaintext, id, HashToken(plaintext), nil
}

// HashToken is the at-rest form of a token.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IsToken reports whether a bearer credential is a rabi API token (vs. an
// OIDC JWT).
func IsToken(bearer string) bool { return strings.HasPrefix(bearer, tokenPrefix) }

// TokenID extracts the lookup id from a presented token.
func TokenID(bearer string) (string, error) {
	rest, ok := strings.CutPrefix(bearer, tokenPrefix)
	if !ok {
		return "", fmt.Errorf("not a rabi API token")
	}
	id, _, ok := strings.Cut(rest, "_")
	if !ok || id == "" {
		return "", fmt.Errorf("malformed rabi API token")
	}
	return id, nil
}

// VerifyTokenHash compares a presented token against a stored hash in
// constant time.
func VerifyTokenHash(presented, storedHash string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(presented)), []byte(storedHash)) == 1
}
