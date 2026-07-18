-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Per-project API tokens (phase1-build-plan.md M1). Only the SHA-256 digest
-- of the full token is stored; the id half of "rabi_<id>_<secret>" is the
-- lookup key. Revocation is a timestamp, never a delete, so audit history
-- keeps its subjects.
CREATE TABLE api_tokens (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    project     text NOT NULL,      -- Phase 0 tenant string; M2 maps to projects
    role        text NOT NULL CHECK (role IN ('viewer','member','operator','admin')),
    token_hash  text NOT NULL,
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at  timestamptz
);

-- Append-only auth audit log: every denied call and every admin action.
-- Append-only is code discipline in M1; the DB-grant enforcement pattern
-- arrives with the M3 accounting ledger and will cover this table too
-- (docs/decisions.md D-035).
CREATE TABLE audit_log (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at             timestamptz NOT NULL DEFAULT now(),
    principal_type text NOT NULL,   -- oidc | token | bootstrap | anonymous
    subject        text NOT NULL,
    principal_name text NOT NULL DEFAULT '',
    role           text NOT NULL DEFAULT '',
    method         text NOT NULL,
    decision       text NOT NULL CHECK (decision IN ('allow','deny')),
    reason         text NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_at_idx ON audit_log (at);
