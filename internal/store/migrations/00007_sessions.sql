-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Sessions (phase1-build-plan.md M6): a scheduler-honored affinity window
-- binding successive tasks to one target. The control-plane session id is
-- what jobs join on (spec.session.join); the adapter's own session id rides
-- along for SubmitTask. Closure/expiry is recorded, never deleted.
CREATE TABLE sessions (
    session_id          text PRIMARY KEY,
    tenant              text NOT NULL,
    target              text NOT NULL,           -- fleet-scoped "<site>/<id>"
    adapter_session_id  text NOT NULL,
    opened_by_job       uuid NOT NULL,
    expires_at          timestamptz,
    closed_at           timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_tenant_idx ON sessions (tenant);
