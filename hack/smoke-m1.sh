#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M1 smoke — job lifecycle over the real stack: submit (with a fleet-format
# warning at this milestone), get, list, cancel via qctl; dry-run enqueues
# nothing; invalid documents rejected with precise errors.
set -euo pipefail
cd "$(dirname "$0")/.."

API_KEY="${TANGLE_API_KEY:-dev-key}"
export TANGLE_API_KEY="$API_KEY"

go build -o bin/qctl ./cmd/qctl

echo "--- dry run validates without enqueuing"
bin/qctl submit -f examples/bell.yaml --dry-run >/dev/null

echo "--- submit + get"
out="$(bin/qctl submit -f examples/bell.yaml)"
job_id="$(echo "$out" | cut -f1)"
phase="$(echo "$out" | cut -f2)"
[ "$phase" = "PENDING" ] || { echo "FAIL: submit phase $phase != PENDING"; exit 1; }
bin/qctl get "$job_id" | grep -q "phase: PENDING" || { echo "FAIL: get did not show PENDING"; exit 1; }

echo "--- list shows the job"
bin/qctl list --tenant demo | grep -q "$job_id" || { echo "FAIL: list missing job"; exit 1; }

echo "--- cancel: PENDING -> CANCELLED"
cout="$(bin/qctl cancel "$job_id")"
[ "$(echo "$cout" | cut -f2)" = "CANCELLED" ] || { echo "FAIL: cancel result: $cout"; exit 1; }

echo "--- terminal job refuses second cancel"
if bin/qctl cancel "$job_id" 2>/dev/null; then
  echo "FAIL: second cancel succeeded on terminal job"; exit 1
fi

echo "--- invalid workload.kind rejected precisely"
sed 's/kind: gate-model/kind: photonic/' examples/bell.yaml > bin/bad-job.yaml
if err="$(bin/qctl submit -f bin/bad-job.yaml 2>&1)"; then
  echo "FAIL: invalid job accepted"; exit 1
fi
echo "$err" | grep -q "/spec/workload/kind" || { echo "FAIL: rejection not precise: $err"; exit 1; }

echo "--- restart tangled: job survives"
docker compose -f deploy/compose/docker-compose.yml restart tangled >/dev/null 2>&1
for i in $(seq 1 30); do
  if bin/qctl get "$job_id" >/dev/null 2>&1; then break; fi
  sleep 1
done
bin/qctl get "$job_id" | grep -q "phase: CANCELLED" || { echo "FAIL: job state lost across restart"; exit 1; }

echo "SMOKE-M1 OK"
