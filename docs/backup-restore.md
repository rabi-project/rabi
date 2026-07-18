# Backup & restore runbook

Rabi's only state is Postgres (one database). The scripted drill in
`hack/backup-drill.sh` executes this runbook end to end — run it after any
change to this document.

## Backup

```sh
pg_dump -U rabi rabi              > backup-db.sql     # the database
pg_dumpall -U rabi --roles-only   > backup-roles.sql  # cluster roles
```

Both files are required. `pg_dump` is database-scoped and does **not**
include the `rabi_app` serving role (cluster-level); without it a restored
cluster refuses to serve (`SET ROLE rabi_app` fails on boot — by design,
that role is what makes the ledger append-only).

Schedule: nightly dump + retain per site policy. The dump is consistent
(single-transaction by default); no downtime needed.

## Restore (fresh cluster)

Order matters — the control plane must not boot before the data is back
(it would migrate an empty database):

1. Start Postgres only.
2. Restore roles: `psql -U rabi rabi < backup-roles.sql`
   (errors about pre-existing roles `rabi`/`postgres` are harmless).
3. Restore the database: `psql -U rabi -v ON_ERROR_STOP=1 rabi < backup-db.sql`.
4. Start `rabi`. Goose sees the restored `goose_db_version` and applies
   only migrations newer than the backup.

## Verify

- `qctl get <job-id>` returns pre-backup jobs; `qctl watch` replays their
  event history to the terminal phase.
- The next reconciliation run logs `reconciliation clean`
  (`RABI_RECONCILE_EVERY=5s` to force one quickly).
- `UPDATE usage_ledger SET ...` as the app role still fails with
  "permission denied" (grants survived the restore).
