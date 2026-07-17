#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# T0.smoke — the empty stack answers over real gRPC (qctl) and real REST.
# Assumes the compose stack is already up (make compose-up).
set -euo pipefail
cd "$(dirname "$0")/.."

API_KEY="${TANGLE_API_KEY:-dev-key}"

echo "--- gRPC via qctl"
go build -o bin/qctl ./cmd/qctl
out="$(TANGLE_API_KEY="$API_KEY" bin/qctl targets)"
echo "$out"
[ "$out" = "0 targets" ] || { echo "FAIL: expected '0 targets', got: $out"; exit 1; }

json="$(TANGLE_API_KEY="$API_KEY" bin/qctl targets -o json)"
[ "$json" = '{"targets":[]}' ] || { echo "FAIL: expected empty JSON list, got: $json"; exit 1; }

echo "--- REST via curl"
rest="$(curl -fsS -H "Authorization: Bearer $API_KEY" http://localhost:8080/v1alpha1/targets)"
echo "$rest"
[ "$rest" = '{"targets":[]}' ] || { echo "FAIL: expected empty JSON list from REST, got: $rest"; exit 1; }

echo "--- auth enforced (missing key -> 401/16)"
code="$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/v1alpha1/targets)"
[ "$code" = "401" ] || { echo "FAIL: expected HTTP 401 without key, got $code"; exit 1; }

if TANGLE_API_KEY="wrong-key" bin/qctl targets 2>/dev/null; then
  echo "FAIL: wrong API key accepted over gRPC"; exit 1
fi

echo "SMOKE OK"
