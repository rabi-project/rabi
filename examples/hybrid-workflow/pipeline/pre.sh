#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Stage 1 — CLASSICAL (runs natively on your scheduler's CPU/GPU). Prepares the
# quantum workload: here it emits a Bell-circuit QuantumJob document, but this is
# where real pipelines generate circuits, sweep parameters, or transpile. No
# quantum resource is touched.
set -eu

WORK="${WORK:-/work}"
SHOTS="${SHOTS:-2000}"
TENANT="${TENANT:-acme/qa}"
mkdir -p "$WORK"

# OPENQASM 3.0 Bell circuit, base64-encoded for inline embedding.
QASM='OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;'
INLINE=$(printf '%s' "$QASM" | base64 | tr -d '\n')

cat > "$WORK/job.yaml" <<YAML
apiVersion: tangle.dev/v1alpha1
kind: QuantumJob
metadata:
  name: hybrid-bell
  tenant: ${TENANT}
spec:
  workload:
    kind: gate-model
    gateModel:
      program:
        format: openqasm3
        inline: ${INLINE}
      shots: ${SHOTS}
  requirements:
    qubits: 2
YAML

echo "pre: wrote $WORK/job.yaml (${SHOTS} shots)"
