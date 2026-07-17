#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# T8.e2e — the operator against a kind cluster and the real compose stack:
#   kubectl apply bell CR  → CR status shows SUCCEEDED (sync lag < 5s bar via
#   2s resync); deleting a CR cancels its control-plane job.
set -euo pipefail
cd "$(dirname "$0")/.."

export RABI_API_KEY="${RABI_API_KEY:-dev-key}"
CLUSTER=rabi-e2e
KCTL="kubectl --context kind-$CLUSTER"

cleanup() {
  [ -n "${OP_PID:-}" ] && kill "$OP_PID" 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "--- compose stack"
docker compose -f deploy/compose/docker-compose.yml up -d --build --wait >/dev/null

echo "--- kind cluster"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --wait 120s >/dev/null

echo "--- CRD"
$KCTL apply -f operator/config/crd.yaml >/dev/null
$KCTL wait --for=condition=Established crd/quantumjobs.tangle.dev --timeout=60s >/dev/null

echo "--- operator (local process against kind + compose)"
(cd operator && go build -o ../bin/rabi-operator .)
KUBECONFIG="$(kind get kubeconfig-path --name "$CLUSTER" 2>/dev/null || true)"
if [ -z "$KUBECONFIG" ]; then
  KUBECONFIG="$HOME/.kube/config"
fi
kind export kubeconfig --name "$CLUSTER" >/dev/null
RABI_API_ADDR=localhost:9090 METRICS_ADDR=:18082 HEALTH_ADDR=:18083 \
  bin/rabi-operator > bin/operator.log 2>&1 &
OP_PID=$!
sleep 3
kill -0 "$OP_PID" || { echo "FAIL: operator died on start"; cat bin/operator.log; exit 1; }

echo "--- kubectl apply bell → SUCCEEDED"
$KCTL apply -f operator/examples/bell.yaml >/dev/null
deadline=$(( $(date +%s) + 180 ))
phase=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  phase="$($KCTL -n demo get quantumjob bell -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break ;; esac
  sleep 2
done
$KCTL -n demo get quantumjobs
[ "$phase" = "SUCCEEDED" ] || { echo "FAIL: CR phase $phase"; cat bin/operator.log | tail -20; exit 1; }

target="$($KCTL -n demo get quantumjob bell -o jsonpath='{.status.boundTarget}')"
reason="$($KCTL -n demo get quantumjob bell -o jsonpath='{.status.placementReason}')"
job_id="$($KCTL -n demo get quantumjob bell -o jsonpath='{.status.jobId}')"
[ -n "$target" ] && [ -n "$reason" ] && [ -n "$job_id" ] || {
  echo "FAIL: status projection incomplete (target=$target reason=$reason job=$job_id)"; exit 1; }
echo "bell SUCCEEDED on $target (job $job_id)"

echo "--- deletion cancels the control-plane job"
cat <<'YAML' | $KCTL apply -f - >/dev/null
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: hold-me, namespace: demo }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: T1BFTlFBU00gMy4wOwppbmNsdWRlICJzdGRnYXRlcy5pbmMiOwpxdWJpdFsyXSBxOwpiaXRbMl0gYzsKaCBxWzBdOwpjeCBxWzBdLCBxWzFdOwpjID0gbWVhc3VyZSBxOwo= }
      shots: 1000
  requirements:
    qubits: 2
    quality: { gateModel: { twoQubitErrorMax: 0.0004 } }   # unsatisfiable: stays PENDING
YAML
deadline=$(( $(date +%s) + 60 ))
held_id=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  held_id="$($KCTL -n demo get quantumjob hold-me -o jsonpath='{.status.jobId}' 2>/dev/null || true)"
  [ -n "$held_id" ] && break
  sleep 2
done
[ -n "$held_id" ] || { echo "FAIL: hold-me never submitted"; exit 1; }

$KCTL -n demo delete quantumjob hold-me --timeout=60s >/dev/null

go build -o bin/qctl ./cmd/qctl
cancelled_phase="$(bin/qctl get "$held_id" -o json | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"]["phase"])')"
[ "$cancelled_phase" = "CANCELLED" ] || {
  echo "FAIL: control-plane job is $cancelled_phase after CR deletion, want CANCELLED"; exit 1; }
echo "deletion cancelled control-plane job $held_id"

echo "E2E-OPERATOR OK"
