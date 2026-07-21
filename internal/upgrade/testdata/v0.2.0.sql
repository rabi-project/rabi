-- SPDX-License-Identifier: Apache-2.0
-- Golden seed for v0.2.0 (goose migration version 3, same schema as v0.1.0).
-- A different data shape: a FAILED job and an in-flight RUNNING job, so the
-- forward-migration matrix exercises a non-terminal task surviving the upgrade.

INSERT INTO jobs (job_id, tenant, name, phase, doc, status, created_at) VALUES
 ('33333333-3333-3333-3333-333333333333', 'globex/prod', 'ghz-1', 'FAILED',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"ghz-1","tenant":"globex/prod"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"FAILED"}', '2025-03-01T09:00:00Z'),
 ('44444444-4444-4444-4444-444444444444', 'globex/prod', 'running-1', 'RUNNING',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"running-1","tenant":"globex/prod"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"RUNNING"}', '2025-03-01T09:10:00Z');

INSERT INTO job_events (job_id, phase, status) VALUES
 ('33333333-3333-3333-3333-333333333333', 'PENDING',   '{"phase":"PENDING"}'),
 ('33333333-3333-3333-3333-333333333333', 'SCHEDULED', '{"phase":"SCHEDULED"}'),
 ('33333333-3333-3333-3333-333333333333', 'FAILED',    '{"phase":"FAILED"}'),
 ('44444444-4444-4444-4444-444444444444', 'PENDING',   '{"phase":"PENDING"}'),
 ('44444444-4444-4444-4444-444444444444', 'SCHEDULED', '{"phase":"SCHEDULED"}'),
 ('44444444-4444-4444-4444-444444444444', 'SUBMITTED', '{"phase":"SUBMITTED"}'),
 ('44444444-4444-4444-4444-444444444444', 'RUNNING',   '{"phase":"RUNNING"}');

INSERT INTO tasks (task_id, job_id, target, adapter_task_id, state) VALUES
 ('bbbbbbbb-4444-4444-4444-444444444444', '44444444-4444-4444-4444-444444444444',
  'sim/aer-2', 'fake-task-42', 'RUNNING');
