#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The recorded demo session (docs/demo.cast / docs/demo.gif). Runs against a
# live compose stack; keep it under ~90 seconds of terminal time.
# Record with:  asciinema rec docs/demo.cast --overwrite -c "bash hack/demo-session.sh"
# GIF with:     agg --speed 1.4 docs/demo.cast docs/demo.gif
set -euo pipefail
cd "$(dirname "$0")/.."
export TANGLE_API_KEY="${TANGLE_API_KEY:-dev-key}"

say() { printf '\n\033[1;36m$ %s\033[0m\n' "$*"; sleep 1; }
run() { say "$*"; "$@"; sleep 2; }

printf '\033[1mRabi — an open-source control plane for quantum compute fleets\033[0m\n'
printf 'Three simulated QPUs replaying real IBM calibration, drifting at 600x wall time.\n'
sleep 2

run bin/qctl targets

say "./deploy/compose/seed.sh   # submit a 20-job mix"
./deploy/compose/seed.sh >/dev/null 2>&1 &
SEED_PID=$!
sleep 3

say "bin/qctl watch --all   # live fleet view"
( bin/qctl watch --all & WATCH_PID=$!; sleep 18; kill $WATCH_PID 2>/dev/null ) || true
wait "$SEED_PID" 2>/dev/null || true
sleep 1

say "bin/qctl list --tenant demo | head -12"
bin/qctl list --tenant demo | head -12
sleep 2

job_id="$(bin/qctl list --tenant demo -o json | python3 -c '
import sys, json
jobs = json.load(sys.stdin)["jobs"]
done = [j for j in jobs if j["status"]["phase"] == "SUCCEEDED"]
print(done[0]["jobId"] if done else jobs[0]["jobId"])')"

say "bin/qctl get $job_id   # placement audit: why did it land there?"
bin/qctl get "$job_id" -o json | python3 -c '
import sys, json
j = json.load(sys.stdin)
p = j["status"].get("placement", {})
print("boundTarget:", j["status"].get("boundTarget"))
print("policy:     ", p.get("policy"))
print("snapshot:   ", p.get("calibrationSnapshot"))
print("reason:     ", p.get("reason", "")[:160])'
sleep 3

run bin/qctl usage --tenant demo

printf '\n\033[1mEvery placement recorded and arguable. github.com/mAengo31/Rabi\033[0m\n'
sleep 2
