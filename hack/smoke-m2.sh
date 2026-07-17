#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M2 smoke — Bell job end-to-end through the compose stack: registered target
# visible, job SUCCEEDED with |00>+|11> dominant counts, placement recorded,
# usage accounted.
set -euo pipefail
cd "$(dirname "$0")/.."

API_KEY="${TANGLE_API_KEY:-dev-key}"
export TANGLE_API_KEY="$API_KEY"

go build -o bin/qctl ./cmd/qctl

echo "--- fleet has the Aer target"
targets=""
for i in $(seq 1 30); do
  targets="$(bin/qctl targets)"
  if echo "$targets" | grep -q "sim/aer-alpha"; then break; fi
  sleep 1
done
echo "$targets" | grep -q "sim/aer-alpha" || { echo "FAIL: target never registered"; exit 1; }

echo "--- submit Bell job and wait for SUCCEEDED"
job_id="$(bin/qctl submit -f examples/bell.yaml | cut -f1)"
phase=""
for i in $(seq 1 60); do
  bin/qctl get "$job_id" -o json > bin/job.json
  phase="$(python3 -c 'import json; print(json.load(open("bin/job.json"))["status"]["phase"])')"
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
  sleep 1
done
[ "$phase" = "SUCCEEDED" ] || { echo "FAIL: job ended $phase"; bin/qctl get "$job_id"; exit 1; }

echo "--- counts histogram is Bell-dominant; placement audit present"
python3 - <<'EOF'
import json
job = json.load(open("bin/job.json"))
task = job["status"]["tasks"][0]
counts = task["result"]["data"]["counts"]
shots = sum(counts.values())
bell = counts.get("00", 0) + counts.get("11", 0)
assert shots == 1000, f"expected 1000 shots, got {shots}"
assert bell / shots > 0.9, f"Bell states only {bell}/{shots}"
placement = job["status"]["placement"]
assert placement["policy"], "placement policy missing"
assert placement["calibrationSnapshot"], "snapshot id missing"
assert placement["reason"], "placement reason missing"
print(f"counts OK: {counts} (bell fraction {bell/shots:.3f})")
print(f"placement: {placement['policy']} on {job['status']['boundTarget']}: {placement['reason']}")
EOF

echo "--- usage recorded (shots >= 1000; ledger accumulates across runs)"
bin/qctl usage --tenant demo -o json > bin/usage.json
python3 - <<'EOF'
import json
usage = json.load(open("bin/usage.json"))["usage"]
shots = sum(u["amount"] for u in usage if u["unit"] == "shots" and u["target"] == "sim/aer-alpha")
assert shots >= 1000, f"expected >= 1000 shots recorded, got {shots}"
print(f"usage OK: {shots} shots on sim/aer-alpha")
EOF

echo "SMOKE-M2 OK"
