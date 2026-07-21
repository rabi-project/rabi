#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Stage 2 — QUANTUM (delegated to Rabi). Submits the QuantumJob via qctl, waits
# for a terminal state, and writes the result counts back for the classical
# post-stage. This is the ONLY stage that touches a quantum resource; Rabi picks
# the device, tracks calibration drift, and records usage. Requires qctl and jq
# on PATH, and RABI_SERVER / RABI_TOKEN in the environment.
set -eu

WORK="${WORK:-/work}"
TIMEOUT="${TIMEOUT:-120}"

job_id=$(qctl submit -f "$WORK/job.yaml" | cut -f1)
echo "quantum: submitted job $job_id to $RABI_SERVER"

elapsed=0
while :; do
  phase=$(qctl get "$job_id" -o json | jq -r '.status.phase')
  case "$phase" in
    SUCCEEDED) break ;;
    FAILED|CANCELLED) echo "quantum: job ended $phase" >&2; exit 1 ;;
  esac
  if [ "$elapsed" -ge "$TIMEOUT" ]; then
    echo "quantum: timed out after ${TIMEOUT}s (last phase $phase)" >&2
    exit 1
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done

qctl get "$job_id" -o json | jq '.status.tasks[0].result.data.counts' > "$WORK/result.json"
echo "quantum: job $job_id SUCCEEDED; counts written to $WORK/result.json"
cat "$WORK/result.json"
