#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# T0.smoke — the stack answers over real gRPC (rabi) and real REST, and the
# single API key is enforced everywhere. (The original M0 "0 targets" check
# applied to the adapterless stack; since M2 the compose fleet has targets,
# so this asserts transport + auth invariants instead.)
set -euo pipefail
cd "$(dirname "$0")/.."

API_KEY="${RABI_TOKEN:-dev-key}"

echo "--- gRPC via rabi"
go build -o bin/rabi ./cmd/rabi
out="$(RABI_TOKEN="$API_KEY" bin/rabi targets)"
echo "$out"

json="$(RABI_TOKEN="$API_KEY" bin/rabi targets -o json)"
echo "$json" | python3 -c 'import sys, json; json.load(sys.stdin)["targets"]' \
  || { echo "FAIL: rabi -o json did not return a targets list"; exit 1; }

echo "--- REST via curl"
rest="$(curl -fsS -H "Authorization: Bearer $API_KEY" http://localhost:8080/v1alpha1/targets)"
echo "$rest" | python3 -c 'import sys, json; json.load(sys.stdin)["targets"]' \
  || { echo "FAIL: REST did not return a targets list"; exit 1; }

echo "--- auth enforced (missing key -> 401/16)"
code="$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/v1alpha1/targets)"
[ "$code" = "401" ] || { echo "FAIL: expected HTTP 401 without key, got $code"; exit 1; }

if RABI_TOKEN="wrong-key" bin/rabi targets 2>/dev/null; then
  echo "FAIL: wrong API key accepted over gRPC"; exit 1
fi

echo "SMOKE OK"
