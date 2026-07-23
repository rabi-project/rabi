#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M1 smoke — job lifecycle over the real stack: submit, get, list, cancel via
# rabi; dry-run enqueues nothing; invalid documents rejected with precise
# errors. Uses an infeasible 60-qubit ask so the job stays PENDING even with
# live adapters (capture-then-grep everywhere: pipefail + grep -q races).
set -euo pipefail
cd "$(dirname "$0")/.."

API_KEY="${RABI_TOKEN:-dev-key}"
export RABI_TOKEN="$API_KEY"

go build -o bin/rabi ./cmd/rabi

echo "--- dry run validates without enqueuing"
bin/rabi submit -f examples/bell.yaml --dry-run >/dev/null

echo "--- submit + get (60-qubit ask: infeasible on this fleet, stays PENDING)"
sed 's/qubits: 2$/qubits: 60/' examples/bell.yaml > bin/pending-job.yaml
out="$(bin/rabi submit -f bin/pending-job.yaml)"
job_id="$(echo "$out" | cut -f1)"
phase="$(echo "$out" | cut -f2)"
[ "$phase" = "PENDING" ] || { echo "FAIL: submit phase $phase != PENDING"; exit 1; }
got="$(bin/rabi get "$job_id")"
echo "$got" | grep -q "phase: PENDING" || { echo "FAIL: get did not show PENDING"; exit 1; }

echo "--- list shows the job"
listing="$(bin/rabi list --tenant demo)"
echo "$listing" | grep -q "$job_id" || { echo "FAIL: list missing job"; exit 1; }

echo "--- cancel: PENDING -> CANCELLED"
cout="$(bin/rabi cancel "$job_id")"
[ "$(echo "$cout" | cut -f2)" = "CANCELLED" ] || { echo "FAIL: cancel result: $cout"; exit 1; }

echo "--- terminal job refuses second cancel"
if bin/rabi cancel "$job_id" 2>/dev/null; then
  echo "FAIL: second cancel succeeded on terminal job"; exit 1
fi

echo "--- invalid workload.kind rejected precisely"
sed 's/kind: gate-model/kind: photonic/' examples/bell.yaml > bin/bad-job.yaml
if err="$(bin/rabi submit -f bin/bad-job.yaml 2>&1)"; then
  echo "FAIL: invalid job accepted"; exit 1
fi
echo "$err" | grep -q "/spec/workload/kind" || { echo "FAIL: rejection not precise: $err"; exit 1; }

echo "--- restart rabi: job survives"
docker compose -f deploy/compose/docker-compose.yml restart rabi >/dev/null 2>&1
for i in $(seq 1 30); do
  if bin/rabi get "$job_id" >/dev/null 2>&1; then break; fi
  sleep 1
done
got="$(bin/rabi get "$job_id")"
echo "$got" | grep -q "phase: CANCELLED" || { echo "FAIL: job state lost across restart"; exit 1; }

echo "SMOKE-M1 OK"
