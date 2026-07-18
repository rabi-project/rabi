-- SPDX-License-Identifier: Apache-2.0
-- +goose Up

-- Tenancy v1 (phase1-build-plan.md M2). The spec API speaks tenant strings
-- (ListJobsRequest.tenant, TenantUsageRequest.tenant — spec law), so the
-- exact wire string is the project's primary key; org/name are derived
-- display fields ("org/rest" split, bare strings get project "default").
-- No UNIQUE(org, name): distinct wire strings may collapse to the same
-- display pair (e.g. "acme" and "acme/default") and identity is the string.
CREATE TABLE projects (
    tenant      text PRIMARY KEY,
    org         text NOT NULL,
    name        text NOT NULL,
    weight      integer NOT NULL DEFAULT 1 CHECK (weight >= 1),
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

-- Per-project quota limits in native units. Enforcement happens at submit
-- time with these rows locked, in the same transaction as the job insert.
CREATE TABLE project_quotas (
    tenant       text NOT NULL REFERENCES projects (tenant) ON DELETE CASCADE,
    unit         text NOT NULL,
    limit_amount double precision NOT NULL CHECK (limit_amount >= 0),
    PRIMARY KEY (tenant, unit)
);

-- declared_cost: a submission's native-unit demand as read from the job
-- document. Only gate-model shots are meterable at admission today; other
-- units meter through the usage ledger after execution.
-- +goose StatementBegin
CREATE FUNCTION declared_cost(doc jsonb, unit text) RETURNS double precision AS $$
  SELECT CASE
    WHEN unit = 'shots'
      THEN COALESCE((doc #>> '{spec,workload,gateModel,shots}')::double precision, 0)
    ELSE 0
  END
$$ LANGUAGE sql IMMUTABLE;
-- +goose StatementEnd

-- Data migration: every tenant string a Phase-0 database ever saw becomes a
-- project row, so nothing existing loses its home (zero-loss criterion).
INSERT INTO projects (tenant, org, name)
SELECT DISTINCT t.tenant,
       split_part(t.tenant, '/', 1),
       CASE WHEN position('/' in t.tenant) > 0
            THEN substring(t.tenant from position('/' in t.tenant) + 1)
            ELSE 'default' END
FROM (
    SELECT tenant FROM jobs
    UNION SELECT tenant FROM usage_ledger
    UNION SELECT project AS tenant FROM api_tokens
) t
WHERE t.tenant <> ''
ON CONFLICT (tenant) DO NOTHING;
