#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M4 acceptance: `helm install` on kind → smoke green. Builds the control
# plane + Aer adapter images, loads them into a kind cluster, installs the
# chart (in-chart Postgres), and runs the API smoke through a port-forward:
# whoami, targets (3 replay QPUs), submit → terminal phase, auth enforced.
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER="${CLUSTER:-rabi-helm-e2e}"
KCTL="kubectl --context kind-$CLUSTER"
TOKEN=helm-e2e-token

cleanup() {
  [ -n "${PF_PID:-}" ] && kill "$PF_PID" 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "--- images"
docker build -t rabi:e2e . >/dev/null
docker build -t rabi-adapter-aer:e2e -f adapters/aer/Dockerfile . >/dev/null

echo "--- kind cluster"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --wait 120s >/dev/null
kind load docker-image rabi:e2e rabi-adapter-aer:e2e --name "$CLUSTER" >/dev/null

echo "--- helm install"
helm install rabi deploy/helm/rabi \
  --kube-context "kind-$CLUSTER" \
  --set image.tag=e2e --set adapterAer.image.tag=e2e \
  --set auth.bootstrapToken="$TOKEN" \
  --wait --timeout 5m >/dev/null

echo "--- port-forward"
$KCTL port-forward svc/rabi 19090:9090 19080:8080 >/dev/null 2>&1 &
PF_PID=$!
sleep 3

go build -o bin/qctl ./cmd/qctl
export RABI_SERVER=localhost:19090 RABI_TOKEN="$TOKEN"

echo "--- whoami (bootstrap admin)"
out="$(bin/qctl whoami)"
echo "$out" | grep -q "bootstrap" || { echo "FAIL: whoami: $out"; exit 1; }

echo "--- targets (replay fleet up)"
deadline=$(( $(date +%s) + 120 ))
count=0
while [ "$(date +%s)" -lt "$deadline" ]; do
  count="$(bin/qctl targets -o json 2>/dev/null | python3 -c 'import sys,json; print(len(json.load(sys.stdin).get("targets",[])))' || echo 0)"
  [ "$count" -ge 3 ] && break
  sleep 3
done
[ "$count" -ge 3 ] || { echo "FAIL: expected >=3 targets, got $count"; exit 1; }

echo "--- submit bell → terminal"
job_id="$(bin/qctl submit -f examples/bell.yaml | cut -f1)"
deadline=$(( $(date +%s) + 180 ))
phase=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  phase="$(bin/qctl get "$job_id" -o json | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"].get("phase",""))')"
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
  sleep 2
done
[ "$phase" = "SUCCEEDED" ] || { echo "FAIL: job phase $phase"; exit 1; }

echo "--- auth enforced"
if RABI_TOKEN=wrong bin/qctl targets >/dev/null 2>&1; then
  echo "FAIL: wrong token accepted"; exit 1
fi

echo "HELM-E2E OK"
