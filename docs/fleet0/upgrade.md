<!-- SPDX-License-Identifier: Apache-2.0 -->
# Fleet-0 upgrade runbook (the rehearsed path)

Fleet-0 is production. Its upgrades follow the path exercised weekly in CI by
the `upgrade` workflow (`internal/upgrade`), never an ad-hoc SSH change. Three
CI-proven guarantees back this procedure:

- **Migrations apply forward from every released schema** with data intact
  (`TestMigrationMatrix`, goldens for v0.1.0 → v0.4.x).
- **A control-plane roll preserves in-flight jobs** — the dispatcher's `resume()`
  re-attaches to running tasks; zero jobs are lost and API unavailability stays
  under 30 s (`TestUpgradeRehearsal`). Measured window in CI: tens of ms.
- **The schema is additive**, so the previous binary runs against the new schema
  — rollback is a binary revert, not a schema downgrade (`TestRollbackSafety`).

Everything runs on one VM via compose; there is a brief API gap while the `rabi`
container is recreated. In-flight adapter tasks keep running (the adapter
container is not restarted) and are re-attached on the new process.

## Before you start

- Confirm the target tag published images: `ghcr.io/rabi-project/rabi:<tag>` and
  `…/rabi-adapter-aer:<tag>` (the `images` workflow does this on tag push).
- Confirm the `upgrade` workflow is green on the commit being released.
- Note the current tag for rollback: `grep RABI_IMAGE_TAG /opt/rabi/repo/deploy/compose/.env`.

## Upgrade

```bash
cd /opt/rabi/repo
COMPOSE="docker compose -f deploy/compose/docker-compose.yml -f deploy/fleet0/compose.images.yml"

# 1. Pin the new version and pull its images (no restart yet).
sed -i "s/^RABI_IMAGE_TAG=.*/RABI_IMAGE_TAG=<new-tag>/" deploy/compose/.env
$COMPOSE pull

# 2. Recreate the control plane. rabi auto-migrates on start (store.Open runs
#    pending migrations, then serves); the migration matrix proves this is safe
#    over existing data. --no-deps so Postgres and the adapter stay up.
$COMPOSE up -d --no-build --no-deps --wait rabi

# 3. Health-gate: the container is only "up" once /healthz passes; confirm and
#    check that jobs are moving (no stuck non-terminal backlog).
curl -fsS localhost:8080/healthz
curl -fsS localhost:8080/metrics | grep rabi_jobs_total
```

If `--wait` times out or `/healthz` fails, roll back (next section) before
investigating.

## Rollback

The schema is additive, so the previous binary serves the new schema unchanged.
Roll the image tag back and recreate — do **not** attempt a schema downgrade.

```bash
cd /opt/rabi/repo
COMPOSE="docker compose -f deploy/compose/docker-compose.yml -f deploy/fleet0/compose.images.yml"
sed -i "s/^RABI_IMAGE_TAG=.*/RABI_IMAGE_TAG=<previous-tag>/" deploy/compose/.env
$COMPOSE pull
$COMPOSE up -d --no-build --no-deps --wait rabi
curl -fsS localhost:8080/healthz
```

The newest migration ships a goose `Down` for true reversibility if a schema
object must ever be removed, but that is not part of a routine rollback: additive
compatibility means the data and schema stay put while only the binary changes.

## After

- Record the upgrade as a status-page annotation once the status page exists
  (P2.M7); until then, note the tag change in the ops log.
- Watch `rabi_jobs_total{phase="PENDING"}` for a few minutes — it should not grow
  unboundedly (the load harness's queue-boundedness check, P2.M2).
