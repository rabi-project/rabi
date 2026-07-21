-- SPDX-License-Identifier: Apache-2.0
-- Golden seed for v0.4.2 (goose migration version 8: adds auth, tenancy,
-- accounting, sessions, probes on top of the v3 core). The migration-critical
-- data is still the job lineage — later migrations are additive and must apply
-- cleanly over a populated jobs/tasks/usage set. Rows span every phase so the
-- forward migration is exercised against the full state taxonomy.

INSERT INTO jobs (job_id, tenant, name, phase, doc, status, created_at) VALUES
 ('55555555-5555-5555-5555-555555555555', 'acme/qa', 'succ', 'SUCCEEDED',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"succ","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"SUCCEEDED"}', '2026-06-01T12:00:00Z'),
 ('66666666-6666-6666-6666-666666666666', 'acme/qa', 'cancelled', 'CANCELLED',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"cancelled","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"CANCELLED"}', '2026-06-01T12:01:00Z'),
 ('77777777-7777-7777-7777-777777777777', 'acme/qa', 'submitted', 'SUBMITTED',
  '{"apiVersion":"tangle.dev/v1alpha1","kind":"QuantumJob","metadata":{"name":"submitted","tenant":"acme/qa"},"spec":{"workload":{"kind":"gate-model"}}}',
  '{"phase":"SUBMITTED"}', '2026-06-01T12:02:00Z');

INSERT INTO job_events (job_id, phase, status) VALUES
 ('55555555-5555-5555-5555-555555555555', 'PENDING',   '{"phase":"PENDING"}'),
 ('55555555-5555-5555-5555-555555555555', 'SCHEDULED', '{"phase":"SCHEDULED"}'),
 ('55555555-5555-5555-5555-555555555555', 'SUBMITTED', '{"phase":"SUBMITTED"}'),
 ('55555555-5555-5555-5555-555555555555', 'RUNNING',   '{"phase":"RUNNING"}'),
 ('55555555-5555-5555-5555-555555555555', 'SUCCEEDED', '{"phase":"SUCCEEDED"}'),
 ('66666666-6666-6666-6666-666666666666', 'PENDING',   '{"phase":"PENDING"}'),
 ('66666666-6666-6666-6666-666666666666', 'CANCELLED', '{"phase":"CANCELLED"}'),
 ('77777777-7777-7777-7777-777777777777', 'PENDING',   '{"phase":"PENDING"}'),
 ('77777777-7777-7777-7777-777777777777', 'SCHEDULED', '{"phase":"SCHEDULED"}'),
 ('77777777-7777-7777-7777-777777777777', 'SUBMITTED', '{"phase":"SUBMITTED"}');

INSERT INTO tasks (task_id, job_id, target, adapter_task_id, state) VALUES
 ('cccccccc-5555-5555-5555-555555555555', '55555555-5555-5555-5555-555555555555',
  'ibm/marrakesh', 'd9egptqneu4c739oej0g', 'SUCCEEDED'),
 ('dddddddd-7777-7777-7777-777777777777', '77777777-7777-7777-7777-777777777777',
  'sim/aer-1', 'fake-task-77', 'SUBMITTED');

INSERT INTO usage_ledger (job_id, task_id, tenant, target, unit, amount) VALUES
 ('55555555-5555-5555-5555-555555555555', 'cccccccc-5555-5555-5555-555555555555',
  'acme/qa', 'ibm/marrakesh', 'shots', 1000),
 ('55555555-5555-5555-5555-555555555555', 'cccccccc-5555-5555-5555-555555555555',
  'acme/qa', 'ibm/marrakesh', 'qpu-seconds', 2);
