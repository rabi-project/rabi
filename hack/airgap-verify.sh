#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M4 acceptance: the offline bundle installs with zero registry egress.
# Every image is loaded from the bundle archive and every pullPolicy is
# Never, so ANY attempted pull fails the install instead of silently
# downloading; afterwards the event log is asserted pull-free and the API
# answers. (The kind+archive+Never construction is the portable equivalent
# of a no-egress network namespace — docs/decisions.md D-038.)
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER="${CLUSTER:-rabi-airgap}"
KCTL="kubectl --context kind-$CLUSTER"
TOKEN=airgap-token

cleanup() {
  [ -n "${PF_PID:-}" ] && kill "$PF_PID" 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "--- bundle"
deploy/airgap/build-bundle.sh verify >/dev/null
tar -C bin -xzf bin/rabi-airgap-verify.tgz

echo "--- kind cluster"
kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
kind create cluster --name "$CLUSTER" --wait 120s >/dev/null

echo "--- offline install (pullPolicy=Never everywhere)"
KIND_CLUSTER="$CLUSTER" bin/rabi-airgap-verify/install.sh rabi "$TOKEN" >/dev/null

echo "--- zero image pulls"
if $KCTL get events -A -o json | python3 -c '
import sys, json
evs = json.load(sys.stdin)["items"]
pulls = [e for e in evs if e.get("reason") == "Pulling"]
print("\n".join(e["message"] for e in pulls))
sys.exit(1 if pulls else 0)'; then
  echo "no pulls observed"
else
  echo "FAIL: image pulls happened during an air-gapped install"; exit 1
fi

echo "--- API answers"
$KCTL port-forward svc/rabi 19091:9090 >/dev/null 2>&1 &
PF_PID=$!
sleep 3
go build -o bin/rabi ./cmd/rabi
out="$(RABI_SERVER=localhost:19091 RABI_TOKEN=$TOKEN bin/rabi whoami)"
echo "$out" | grep -q bootstrap || { echo "FAIL: whoami: $out"; exit 1; }

echo "AIRGAP-VERIFY OK"
