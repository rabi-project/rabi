#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Artifact A seed: submit a 20-job mix that exercises the whole demo —
# routed successes across the replay fleet, quality floors (satisfiable and
# not), an infeasible width ask, invalid programs (error taxonomy), deadlines,
# and cancellations. End state: SUCCEEDED, FAILED (with category), CANCELLED,
# and PENDING-infeasible (with recorded reason) all present (T7.seed).
#
# Usage: ./deploy/compose/seed.sh   (stack must be up; RABI_API_KEY set or dev-key)
set -euo pipefail
cd "$(dirname "$0")/../.."

export RABI_API_KEY="${RABI_API_KEY:-dev-key}"
QCTL="bin/qctl"
go build -o "$QCTL" ./cmd/qctl

b64() { printf '%s' "$1" | base64 | tr -d '\n'; }

bell='OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;'

ghz() { # ghz N — linear GHZ chain
  local n="$1"
  local prog="OPENQASM 3.0;
include \"stdgates.inc\";
qubit[$n] q;
bit[$n] c;
h q[0];"
  for ((i = 1; i < n; i++)); do prog+="
cx q[$((i - 1))], q[$i];"; done
  prog+="
c = measure q;"
  printf '%s' "$prog"
}

submit() { # submit NAME QUBITS SHOTS PROGRAM [EXTRA_SPEC_YAML]
  local name="$1" qubits="$2" shots="$3" program="$4" extra="${5:-}"
  {
    cat <<YAML
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata: { name: ${name}, tenant: demo }
spec:
  workload:
    kind: gate-model
    gateModel:
      program: { format: openqasm3, inline: $(b64 "$program") }
      shots: ${shots}
YAML
    # An extra block that declares its own requirements replaces the default.
    case "$extra" in
      *"requirements:"*) ;;
      *) printf '  requirements:\n    qubits: %s\n' "$qubits" ;;
    esac
    if [ -n "$extra" ]; then
      printf '%s\n' "$extra"
    fi
  } | "$QCTL" submit -f - | cut -f1
}

echo "--- seeding 20 jobs"
ids=()

# 1-8: routed successes — mixed widths and shots across the replay fleet.
ids+=("$(submit bell-a 2 1000 "$bell")")
ids+=("$(submit bell-b 2 4000 "$bell")")
ids+=("$(submit ghz3 3 2000 "$(ghz 3)")")
ids+=("$(submit ghz5 5 1000 "$(ghz 5)")")
ids+=("$(submit ghz8 8 2000 "$(ghz 8)")")
ids+=("$(submit ghz12 12 500 "$(ghz 12)")")
ids+=("$(submit ghz16 16 1000 "$(ghz 16)")")
ids+=("$(submit ghz20 20 500 "$(ghz 20)")")

# 9-11: quality floors the fleet can satisfy (calib-aware picks the target
# whose live calibration clears them).
ids+=("$(submit floor-ok-a 2 1000 "$bell" '  requirements:
    qubits: 2
    quality: { gateModel: { twoQubitErrorMax: 0.02 } }')")
ids+=("$(submit floor-ok-b 4 1000 "$(ghz 4)" '  requirements:
    qubits: 4
    quality: { gateModel: { readoutErrorMax: 0.08 } }')")
ids+=("$(submit deadline-job 2 1000 "$bell" "  deadline: \"$(date -u -v+1H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+1 hour' '+%Y-%m-%dT%H:%M:%SZ')\"")")

# 12-13: floors below any device's physics — stays PENDING with the reason
# recorded (the fleet may improve after a calibration event).
ids+=("$(submit floor-impossible 2 1000 "$bell" '  requirements:
    qubits: 2
    quality: { gateModel: { twoQubitErrorMax: 0.0005 } }')")
ids+=("$(submit fresh-cal-only 2 1000 "$bell" '  requirements:
    qubits: 2
    quality: { gateModel: { calibrationMaxAge: 1ms } }')")

# 14: wider than any device — PENDING-infeasible with per-target reasons.
ids+=("$(submit too-wide 25 1000 "$(ghz 25)")")

# 15-16: invalid programs — FAILED with INVALID_PROGRAM category.
ids+=("$(submit bad-syntax 2 1000 'OPENQASM 3.0; this is not a program;')")
ids+=("$(submit half-a-program 2 1000 'OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
h q[')")

# 17-18: denied by selector — PENDING with selector reasons.
ids+=("$(submit denied-everywhere 2 1000 "$bell" '  backendSelector:
    denyTargets: [sim/ibm-torino-r, sim/ibm-sherbrooke-r, sim/ibm-brisbane-r]')")
ids+=("$(submit require-missing 2 1000 "$bell" '  backendSelector:
    requireTargets: [onprem/does-not-exist]')")

# 19-20: submitted then cancelled (held PENDING by an unsatisfiable floor so
# the cancellation is deterministic, not racing the simulator).
ids+=("$(submit cancel-me-a 2 1000 "$bell" '  requirements:
    qubits: 2
    quality: { gateModel: { twoQubitErrorMax: 0.0006 } }')")
ids+=("$(submit cancel-me-b 8 4000 "$(ghz 8)" '  requirements:
    qubits: 8
    quality: { gateModel: { twoQubitErrorMax: 0.0006 } }')")
"$QCTL" cancel "${ids[18]}" >/dev/null
"$QCTL" cancel "${ids[19]}" >/dev/null

echo "--- 20 jobs seeded. Live view: $QCTL watch --all"
echo "--- summary (states settle within ~30s):"
sleep 5
"$QCTL" list --tenant demo | head -25
