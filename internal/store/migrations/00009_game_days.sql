-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Game-day drills (phase2-build-plan.md P2.M1/M7): each supervised chaos
-- exercise records a row here — when it ran, what scenario, whether the
-- invariants held, and the operator note. The M7 status page renders the most
-- recent one ("last game-day date and result"). Append-only like every other
-- operational record; drills are never rewritten after the fact.
CREATE TABLE game_days (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    scenario     text NOT NULL,           -- "invariant-sweep", "adapter-kill", ...
    target       text NOT NULL,           -- "compose", "fleet0"
    invariants_green boolean,             -- null until finished
    violations   integer NOT NULL DEFAULT 0,
    operator     text NOT NULL DEFAULT '',
    note         text NOT NULL DEFAULT ''
);
CREATE INDEX game_days_started_idx ON game_days (started_at DESC);
REVOKE UPDATE, DELETE, TRUNCATE ON game_days FROM rabi_app;
-- The driver writes one finalized row when a drill completes (both timestamps
-- and the result in a single INSERT), so the record is append-only end to end —
-- no drill is ever rewritten after it ran.
