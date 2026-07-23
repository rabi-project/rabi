#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M11 acceptance driver: seeded compose stack + Playwright console e2e
# (hack/e2e-console.mjs). Node + npx fetch playwright at TEST time — the
# console itself has zero dependencies and runtime never needs internet.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
export RABI_TOKEN="${RABI_TOKEN:-dev-key}"
mkdir -p bin

cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "--- stack up"
$COMPOSE up -d --build --wait >/dev/null

echo "--- a bound job for the placement-audit page (with a real rejection)"
go build -o bin/rabi ./cmd/rabi
python3 - > bin/console-job.json <<'PY'
import json, base64
qasm = ("OPENQASM 3.0;\ninclude \"stdgates.inc\";\nqubit[2] q;\nbit[2] c;\n"
        "h q[0];\ncx q[0], q[1];\nc = measure q;\n")
print(json.dumps({
    "apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
    "metadata": {"name": "console-audit", "tenant": "demo"},
    "spec": {
        "workload": {"kind": "gate-model", "gateModel": {
            "program": {"format": "openqasm3",
                        "inline": base64.b64encode(qasm.encode()).decode()},
            "shots": 500}},
        # Deny one replay target so the audit page has a rejected entry.
        "backendSelector": {"denyTargets": ["sim/ibm-torino-r"]},
    },
}))
PY
job_id="$(bin/rabi submit -f bin/console-job.json | cut -f1)"
# The seeded demo mix queues ahead of this job on the replay targets;
# slow shared runners need the long leash.
for _ in $(seq 1 300); do
  phase="$(bin/rabi get "$job_id" -o json | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"].get("phase",""))')"
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
  sleep 1
done
[ "$phase" = "SUCCEEDED" ] || { echo "FAIL: seed job ended $phase"; exit 1; }

# Seed the demo mix AFTER the audit job is terminal: on 2-core runners the
# mix takes minutes to drain and would queue ahead of it otherwise. The
# playwright pass only needs populated views, not drained ones.
echo "--- seed demo mix"
./deploy/compose/seed.sh >/dev/null

echo "--- playwright"
if [ ! -d node_modules/playwright ]; then
  npm install --no-save playwright >/dev/null 2>&1
  npx playwright install --with-deps chromium >/dev/null 2>&1 || npx playwright install chromium >/dev/null
fi
node hack/e2e-console.mjs "http://localhost:8080" "$RABI_TOKEN" "$job_id"
