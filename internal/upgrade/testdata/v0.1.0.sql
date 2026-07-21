-- SPDX-License-Identifier: Apache-2.0
-- Golden seed for v0.1.0 (goose migration version 3: jobs, tasks, usage_ledger,
-- job_events). Representative early-era data: a terminal job with usage and a
-- job still pending. Column set is the one that shipped at v3 and is unchanged
-- since, so the same rows are what a real v0.1.0 database would carry.

INSERT INTO jobs (job_id, tenant, name, phase, doc, status, created_at) VALUES
 ('11111111-1111-1111-1111-111111111111', 'acme/qa', 'bell-1', 'SUCCEEDED',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"bell-1","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"SUCCEEDED"}', '2025-01-10T10:00:00Z'),
 ('22222222-2222-2222-2222-222222222222', 'acme/qa', 'pending-1', 'PENDING',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"pending-1","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"PENDING"}', '2025-01-10T10:05:00Z');

INSERT INTO job_events (job_id, phase, status) VALUES
 ('11111111-1111-1111-1111-111111111111', 'PENDING',   '{"phase":"PENDING"}'),
 ('11111111-1111-1111-1111-111111111111', 'SCHEDULED', '{"phase":"SCHEDULED"}'),
 ('11111111-1111-1111-1111-111111111111', 'SUBMITTED', '{"phase":"SUBMITTED"}'),
 ('11111111-1111-1111-1111-111111111111', 'RUNNING',   '{"phase":"RUNNING"}'),
 ('11111111-1111-1111-1111-111111111111', 'SUCCEEDED', '{"phase":"SUCCEEDED"}'),
 ('22222222-2222-2222-2222-222222222222', 'PENDING',   '{"phase":"PENDING"}');

INSERT INTO tasks (task_id, job_id, target, adapter_task_id, state) VALUES
 ('aaaaaaaa-1111-1111-1111-111111111111', '11111111-1111-1111-1111-111111111111',
  'sim/aer-1', 'fake-task-1', 'SUCCEEDED');

INSERT INTO usage_ledger (job_id, task_id, tenant, target, unit, amount) VALUES
 ('11111111-1111-1111-1111-111111111111', 'aaaaaaaa-1111-1111-1111-111111111111',
  'acme/qa', 'sim/aer-1', 'shots', 1000),
 ('11111111-1111-1111-1111-111111111111', 'aaaaaaaa-1111-1111-1111-111111111111',
  'acme/qa', 'sim/aer-1', 'tasks', 1);
