-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Shadow placements (phase2-build-plan.md P2.M5): for every real scheduling
-- decision, each candidate ("shadow") policy computes the placement it WOULD
-- have made — recorded here, never executed. The promotion pipeline compares a
-- candidate against the active policy from these rows: agreement rate and the
-- fidelity-proxy / wait deltas (with confidence intervals) over a window of
-- live fleet-0 operation. Append-only like every other measurement table; a
-- shadow decision is a fact about what was computed, never rewritten.
CREATE TABLE shadow_placements (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at            timestamptz NOT NULL DEFAULT now(),
    job_id        uuid NOT NULL,
    tenant        text NOT NULL DEFAULT '',
    policy        text NOT NULL,              -- the candidate/shadow policy name
    active_policy text NOT NULL,              -- the policy that actually bound
    active_target text NOT NULL DEFAULT '',   -- what the active policy chose ('' = none feasible)
    shadow_target text NOT NULL DEFAULT '',   -- what the candidate would choose ('' = none feasible)
    agreed        boolean NOT NULL,           -- active_target == shadow_target
    active_esp    double precision,           -- fidelity proxy (predicted ESP) of the active choice
    shadow_esp    double precision,           -- fidelity proxy of the candidate choice
    active_wait   double precision,           -- queue-depth wait proxy of the active choice
    shadow_wait   double precision            -- queue-depth wait proxy of the candidate choice
);
CREATE INDEX shadow_placements_policy_at_idx ON shadow_placements (policy, at DESC);
REVOKE UPDATE, DELETE, TRUNCATE ON shadow_placements FROM rabi_app;

-- +goose Down
DROP TABLE IF EXISTS shadow_placements;
