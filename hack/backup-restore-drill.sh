#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Backup → restore drill (phase2-build-plan.md P2.M7): pg_dump the live database,
# restore it into a fresh database, and verify the restore is faithful (row
# counts of the measurement tables match). Scripted so it runs monthly in CI and
# on fleet-0; the JSON result is published as an artifact ("results published").
#
#   SRC_DSN=postgres://rabi:rabi@localhost:5432/rabi?sslmode=disable \
#     hack/backup-restore-drill.sh [--out drill.json]
#
# Requires pg_dump and psql on PATH (postgresql-client). Exits non-zero if the
# restore does not match — a failed drill blocks nothing on its own but is loud.
set -uo pipefail

OUT=""
if [ "${1:-}" = "--out" ]; then OUT="${2:-}"; fi

SRC="${SRC_DSN:-}"
if [ -z "$SRC" ]; then
  echo "SRC_DSN is required" >&2
  exit 2
fi

# Derive an admin DSN (the 'postgres' maintenance db) and a fresh restore db name
# on the same server, parsing the source DSN.
proto_removed="${SRC#*://}"
creds_host="${proto_removed%%/*}"          # user:pass@host:port
base="postgres://${creds_host}"
suffix=""
case "$SRC" in *\?*) suffix="?${SRC#*\?}";; esac
RESTORE_DB="rabi_drill_$$"
ADMIN="${base}/postgres${suffix}"
RESTORE="${base}/${RESTORE_DB}${suffix}"

dump="$(mktemp)"
cleanup() {
  rm -f "$dump"
  psql "$ADMIN" -q -c "DROP DATABASE IF EXISTS ${RESTORE_DB};" >/dev/null 2>&1 || true
}
trap cleanup EXIT

started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "backup-restore drill: dumping source…"
if ! pg_dump "$SRC" > "$dump"; then
  echo "pg_dump failed" >&2; exit 1
fi

echo "restoring into ${RESTORE_DB}…"
psql "$ADMIN" -q -c "CREATE DATABASE ${RESTORE_DB};" || { echo "createdb failed" >&2; exit 1; }
if ! psql "$RESTORE" -q -v ON_ERROR_STOP=1 -f "$dump" >/dev/null; then
  echo "restore failed" >&2; exit 1
fi

# Verify: measurement tables must have identical row counts.
tables="jobs job_events usage_ledger tasks"
ok=1
details=""
for t in $tables; do
  a="$(psql "$SRC" -tAc "SELECT count(*) FROM ${t};" 2>/dev/null || echo NA)"
  b="$(psql "$RESTORE" -tAc "SELECT count(*) FROM ${t};" 2>/dev/null || echo NA)"
  if [ "$a" != "$b" ]; then
    ok=0
    echo "  MISMATCH ${t}: source=${a} restore=${b}"
  else
    echo "  ${t}: ${a} rows match"
  fi
  details="${details}{\"table\":\"${t}\",\"source\":\"${a}\",\"restore\":\"${b}\"},"
done
finished="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
verified=$([ "$ok" -eq 1 ] && echo true || echo false)

result="{\"drill\":\"backup-restore\",\"started_at\":\"${started}\",\"finished_at\":\"${finished}\",\"verified\":${verified},\"tables\":[${details%,}]}"
echo "$result"
if [ -n "$OUT" ]; then echo "$result" > "$OUT"; fi

if [ "$ok" -eq 1 ]; then
  echo "PASS: restore is faithful"
  exit 0
fi
echo "FAIL: restore did not match source" >&2
exit 1
