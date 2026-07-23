#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M12 acceptance: probe jobs run on a schedule under the system tenant and
# their results are visible in /metrics (fidelity + estimator error + age).
set -euo pipefail
cd "$(dirname "$0")/.."

COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
export RABI_TOKEN="${RABI_TOKEN:-dev-key}" RABI_PROBE_EVERY=10s
mkdir -p bin

cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "--- stack up (probes every 10s)"
$COMPOSE up -d --build --wait >/dev/null

echo "--- waiting for probe metrics"
deadline=$(( $(date +%s) + 240 ))
ok=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  metrics="$(curl -s http://localhost:8080/metrics || true)"
  if echo "$metrics" | grep -q '^rabi_probe_fidelity{' ; then ok=1; break; fi
  sleep 5
done
[ -n "$ok" ] || { echo "FAIL: no probe fidelity metric appeared"; echo "$metrics" | head; exit 1; }

echo "$metrics" | grep '^rabi_probe_fidelity{' | head -3
fid="$(echo "$metrics" | grep '^rabi_probe_fidelity{' | head -1 | awk '{print $2}')"
python3 -c "
f = float('$fid')
assert 0.5 <= f <= 1.0, f'probe fidelity {f} out of range'
print(f'probe fidelity {f} plausible')"

echo "$metrics" | grep -q '^rabi_probe_age_seconds{' || { echo "FAIL: no probe age metric"; exit 1; }

echo "--- probes attributed to the system tenant"
go build -o bin/rabi ./cmd/rabi
n="$(bin/rabi list --tenant system/probes -o json | python3 -c 'import sys,json; print(len(json.load(sys.stdin)["jobs"]))')"
[ "$n" -ge 1 ] || { echo "FAIL: no jobs under system/probes"; exit 1; }
echo "system/probes jobs: $n"

echo "PROBES-E2E OK"
