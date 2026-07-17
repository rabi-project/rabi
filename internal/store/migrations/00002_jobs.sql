-- SPDX-License-Identifier: Apache-2.0
-- +goose Up
CREATE TABLE jobs (
    job_id     uuid PRIMARY KEY,
    tenant     text NOT NULL,
    name       text NOT NULL,
    phase      text NOT NULL,
    doc        jsonb NOT NULL,   -- the QuantumJob document as accepted
    status     jsonb NOT NULL,   -- status object per spec/spec/quantumjob.md
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_tenant_created_idx ON jobs (tenant, created_at DESC, job_id);
CREATE INDEX jobs_phase_idx ON jobs (phase);

-- Every phase transition (and initial admission) appends an event; watchers
-- replay events in seq order, so no transition is ever missed or reordered.
CREATE TABLE job_events (
    seq        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id     uuid NOT NULL REFERENCES jobs (job_id),
    phase      text NOT NULL,
    status     jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_events_job_seq_idx ON job_events (job_id, seq);

-- +goose Down
DROP TABLE job_events;
DROP TABLE jobs;
