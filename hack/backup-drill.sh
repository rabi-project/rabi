#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M4 acceptance: scripted backup → destroy → restore drill. Ends with the
# restored job intact, watch streams replaying history, and a clean
# reconciliation run. Mirrors docs/backup-restore.md — keep them in sync.
#
# Gotcha the drill exercises deliberately: pg_dump is database-scoped and
# does NOT carry the cluster-level rabi_app role, so a bare restore into a
# fresh cluster would break SET ROLE. Roles are dumped separately.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
export RABI_TOKEN="${RABI_TOKEN:-dev-key}"
export RABI_RECONCILE_EVERY=5s
mkdir -p bin

echo "--- stack up + seed one finished job"
$COMPOSE up -d --build --wait >/dev/null
go build -o bin/qctl ./cmd/qctl
job_id="$(bin/qctl submit -f examples/bell.yaml | cut -f1)"
phase=""
for _ in $(seq 1 90); do
  phase="$(bin/qctl get "$job_id" -o json | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"].get("phase",""))')"
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
  sleep 1
done
[ "$phase" = "SUCCEEDED" ] || { echo "FAIL: seed job ended $phase"; exit 1; }

echo "--- backup (database + cluster roles)"
$COMPOSE exec -T postgres pg_dump -U rabi rabi > bin/backup-db.sql
$COMPOSE exec -T postgres pg_dumpall -U rabi --roles-only > bin/backup-roles.sql

echo "--- destroy (volumes wiped)"
$COMPOSE down -v >/dev/null

echo "--- restore: postgres first, roles, then the database, then rabi"
$COMPOSE up -d --wait postgres >/dev/null
# Pre-existing roles (rabi, postgres) error harmlessly; rabi_app is created.
$COMPOSE exec -T postgres psql -U rabi rabi < bin/backup-roles.sql >/dev/null 2>&1 || true
$COMPOSE exec -T postgres psql -U rabi -v ON_ERROR_STOP=1 rabi < bin/backup-db.sql >/dev/null
$COMPOSE up -d --wait >/dev/null

echo "--- restored job intact + watch stream replays to terminal"
got="$(bin/qctl get "$job_id" -o json | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"].get("phase",""))')"
[ "$got" = "SUCCEEDED" ] || { echo "FAIL: restored job phase $got"; exit 1; }
watch_out="$(bin/qctl watch "$job_id" 2>/dev/null | tail -1)"
echo "$watch_out" | grep -q "SUCCEEDED" || { echo "FAIL: watch did not replay to terminal: $watch_out"; exit 1; }

echo "--- reconciliation clean on the restored database"
deadline=$(( $(date +%s) + 60 ))
ok=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  logs="$($COMPOSE logs rabi 2>/dev/null | tail -50)"
  if echo "$logs" | grep -q "reconciliation clean"; then ok=1; break; fi
  if echo "$logs" | grep -q "reconciliation found mismatches"; then
    echo "FAIL: reconciliation found mismatches after restore"; exit 1
  fi
  sleep 3
done
[ -n "$ok" ] || { echo "FAIL: no reconciliation run observed"; exit 1; }

$COMPOSE down -v >/dev/null
echo "BACKUP-DRILL OK"
