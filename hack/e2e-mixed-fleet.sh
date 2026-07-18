#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M10 acceptance: a fleet of 3 replay + 1 cloud (IQM cassette) + 1 GPU-class
# simulator target schedules a mixed workload — every job terminal SUCCEEDED
# and each fleet segment actually used.
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f deploy/compose/docker-compose.yml --profile mixed"
export RABI_TOKEN="${RABI_TOKEN:-dev-key}"
export RABI_ADAPTERS_EXTRA=",iqm=adapter-iqm:50055,gpu=adapter-gpu:50056"
mkdir -p bin

cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "--- mixed fleet up (3 replay + iqm cassette + gpu-class sim)"
$COMPOSE up -d --build --wait >/dev/null
go build -o bin/qctl ./cmd/qctl

targets="$(bin/qctl targets -o json)"
count="$(echo "$targets" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["targets"]))')"
[ "$count" -ge 5 ] || { echo "FAIL: expected >=5 targets, got $count"; echo "$targets"; exit 1; }

submit_variant() { # name, extra-spec-python
  python3 - "$1" "$2" > "bin/mixed-$1.yaml" <<'PY'
import sys, json, base64
name, extra = sys.argv[1], sys.argv[2]
qasm = "OPENQASM 3.0;\ninclude \"stdgates.inc\";\nqubit[2] q;\nbit[2] c;\nh q[0];\ncx q[0], q[1];\nc = measure q;\n"
doc = {
    "apiVersion": "tangle.dev/v1alpha1", "kind": "QuantumJob",
    "metadata": {"name": f"mixed-{name}", "tenant": "mixed/e2e"},
    "spec": {
        "workload": {"kind": "gate-model", "gateModel": {
            "program": {"format": "openqasm3",
                        "inline": base64.b64encode(qasm.encode()).decode()},
            "shots": 500}},
    },
}
doc["spec"].update(eval(extra))
print(json.dumps(doc))
PY
  bin/qctl submit -f "bin/mixed-$1.yaml" | cut -f1
}

echo "--- mixed workload"
ids=()
ids+=("$(submit_variant replay1 '{}')")
ids+=("$(submit_variant replay2 '{}')")
ids+=("$(submit_variant cloud1 '{"backendSelector": {"requireTargets": ["iqm/cassette-iqm"], "allowCloudBurst": ["iqm/cassette-iqm"]}}')")
ids+=("$(submit_variant gpu1 '{"backendSelector": {"requireTargets": ["gpu/gpu-sim-1"]}}')")
ids+=("$(submit_variant gpu2 '{"requirements": {"technology": ["simulator"]}}')")  # RFC-0001 filter: only the sim qualifies

declare -a bounds=()
for id in "${ids[@]}"; do
  phase=""
  for _ in $(seq 1 120); do
    bin/qctl get "$id" -o json > bin/mixed-job.json
    phase="$(python3 -c 'import json; print(json.load(open("bin/mixed-job.json"))["status"].get("phase",""))')"
    case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
    sleep 1
  done
  [ "$phase" = "SUCCEEDED" ] || { echo "FAIL: job $id ended $phase"; bin/qctl get "$id"; exit 1; }
  bounds+=("$(python3 -c 'import json; print(json.load(open("bin/mixed-job.json"))["status"].get("boundTarget",""))')")
done

echo "placements: ${bounds[*]}"
printf '%s\n' "${bounds[@]}" | grep -q "^sim/" || { echo "FAIL: no replay placement"; exit 1; }
printf '%s\n' "${bounds[@]}" | grep -q "^iqm/cassette-iqm$" || { echo "FAIL: no cloud placement"; exit 1; }
printf '%s\n' "${bounds[@]}" | grep -qc "^gpu/gpu-sim-1$" || { echo "FAIL: no gpu placement"; exit 1; }
[ "$(printf '%s\n' "${bounds[@]}" | grep -c '^gpu/gpu-sim-1$')" -ge 2 ] || { echo "FAIL: 12q job missed the sim"; exit 1; }

echo "MIXED-FLEET-E2E OK"
