-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- One task = one submission of a payload to one target. task_id doubles as
-- the idempotency key sent to adapters, so a control-plane restart can
-- resubmit without duplicating execution.
CREATE TABLE tasks (
    task_id         uuid PRIMARY KEY,
    job_id          uuid NOT NULL REFERENCES jobs (job_id),
    target          text NOT NULL,      -- fleet-scoped "<site>/<target_id>"
    adapter_task_id text,
    state           text NOT NULL,
    error           jsonb,
    result          jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tasks_job_idx ON tasks (job_id);

-- Native-unit usage ledger: append-only. UNIQUE (task_id, unit) makes usage
-- recording idempotent — re-observing a terminal task cannot double-bill.
CREATE TABLE usage_ledger (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id      uuid NOT NULL,
    task_id     uuid NOT NULL,
    tenant      text NOT NULL,
    target      text NOT NULL,
    unit        text NOT NULL,
    amount      double precision NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, unit)
);
CREATE INDEX usage_tenant_time_idx ON usage_ledger (tenant, recorded_at);

-- +goose Down
DROP TABLE usage_ledger;
DROP TABLE tasks;
