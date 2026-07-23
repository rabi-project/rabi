#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# T7.seed — the seeded 20-job demo mix settles into the full state taxonomy:
# >=1 SUCCEEDED, >=1 FAILED with an error category, >=1 CANCELLED, and >=1
# PENDING-infeasible with a recorded reason. Also asserts the IBM adapter is
# provably dormant without its profile (T7.ibm-flag, tokenless half).
set -euo pipefail
cd "$(dirname "$0")/.."

export RABI_TOKEN="${RABI_TOKEN:-dev-key}"
go build -o bin/rabi ./cmd/rabi

echo "--- IBM adapter dormant without profile"
services="$(docker compose -f deploy/compose/docker-compose.yml config --services)"
if echo "$services" | grep -q adapter-ibm; then
  echo "FAIL: adapter-ibm active without --profile ibm"; exit 1
fi
services_ibm="$(docker compose -f deploy/compose/docker-compose.yml --profile ibm config --services)"
echo "$services_ibm" | grep -q adapter-ibm || { echo "FAIL: ibm profile missing"; exit 1; }

echo "--- seed the 20-job mix"
./deploy/compose/seed.sh >/dev/null

echo "--- wait for states to settle"
deadline=$(( $(date +%s) + 300 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  bin/rabi list --tenant demo -o json > bin/demo-jobs.json
  if python3 - <<'EOF'
import json, sys
jobs = json.load(open("bin/demo-jobs.json")).get("jobs", [])
phases = {}
for j in jobs:
    phases.setdefault(j["status"]["phase"], []).append(j)
needed = {"SUCCEEDED", "FAILED", "CANCELLED", "PENDING"}
sys.exit(0 if needed <= set(phases) and
         not any(p in phases for p in ("SCHEDULED", "SUBMITTED", "RUNNING")) else 1)
EOF
  then break; fi
  sleep 5
done

echo "--- assert the taxonomy"
python3 - <<'EOF'
import json
jobs = json.load(open("bin/demo-jobs.json")).get("jobs", [])
phases = {}
for j in jobs:
    phases.setdefault(j["status"]["phase"], []).append(j)
for needed in ("SUCCEEDED", "FAILED", "CANCELLED", "PENDING"):
    assert phases.get(needed), f"no job ended {needed}"

failed = phases["FAILED"][0]["status"]
category = failed["tasks"][0]["error"]["category"]
assert category, "FAILED job lacks an error category"

pending_reasons = [
    c.get("message", "")
    for j in phases["PENDING"]
    for c in j["status"].get("conditions", [])
    if c.get("type") == "Schedulable"
]
assert any("no feasible target" in r for r in pending_reasons), \
    f"no PENDING-infeasible reason recorded: {pending_reasons}"

print(f"taxonomy OK: " + ", ".join(f"{k}={len(v)}" for k, v in sorted(phases.items())))
print(f"FAILED category: {category}")
EOF

echo "SMOKE-M7 OK"
