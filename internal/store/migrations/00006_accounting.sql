-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Accounting v1 (phase1-build-plan.md M3): the ledger's append-only property
-- is enforced by the DATABASE, not code discipline. Migrations run as the
-- owning user; the server then serves every connection under the rabi_app
-- role, which simply has no UPDATE/DELETE/TRUNCATE privilege on the ledger
-- and audit tables. This closes D-035's interim.

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'rabi_app') THEN
    CREATE ROLE rabi_app NOLOGIN;
  END IF;
END $$;
-- +goose StatementEnd

-- Reconciliation history: one row per run (weekly in production).
CREATE TABLE reconciliation_runs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    checked    bigint NOT NULL,
    mismatches jsonb NOT NULL DEFAULT '[]'
);

GRANT USAGE ON SCHEMA public TO rabi_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO rabi_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO rabi_app;
-- Future tables/sequences created by later migrations inherit app access.
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO rabi_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO rabi_app;

-- The point of this migration: append-only at the privilege level.
REVOKE UPDATE, DELETE, TRUNCATE ON usage_ledger FROM rabi_app;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM rabi_app;
REVOKE UPDATE, DELETE, TRUNCATE ON reconciliation_runs FROM rabi_app;
REVOKE UPDATE, DELETE, TRUNCATE ON job_events FROM rabi_app;

-- Let the migrating user assume the serving role.
-- +goose StatementBegin
DO $$
BEGIN
  EXECUTE format('GRANT rabi_app TO %I', current_user);
END $$;
-- +goose StatementEnd
