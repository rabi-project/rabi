#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Mutation testing for the scheduler and the job FSM (phase2-build-plan.md
# P2.M4). gremlins 0.5.0 does not run under this module's Go toolchain (its
# module-wide baseline pulls in the testcontainer suites), so this is a small,
# reliable, curated harness instead: each mutant is a single semantic edit to a
# load-bearing line; a mutant is KILLED when the pure package tests fail with it
# applied, and SURVIVED when they still pass. A surviving mutant is a gap in the
# test suite. Efficacy = killed / viable must meet the floor.
#
# Both target packages are pure (no Postgres, no Docker), so this runs anywhere
# `go test` does.
set -uo pipefail
cd "$(dirname "$0")/.."

PKGS=(./internal/job/ ./internal/scheduler/)
FLOOR=65

# mutant := "file::old::new" — old must be a unique substring of file.
MUTANTS=(
  # FSM (internal/job/phase.go)
  "internal/job/phase.go::if from.Terminal() {::if !from.Terminal() {"
  "internal/job/phase.go::return transitions[from][to]::return !transitions[from][to]"
  "internal/job/phase.go::return ok && len(next) == 0::return ok && len(next) != 0"
  "internal/job/phase.go::Running:   {Succeeded: true::Running:   {Succeeded: false"
  # Scheduler filter (internal/scheduler/filter.go)
  "internal/scheduler/filter.go::if !t.Online {::if t.Online {"
  "internal/scheduler/filter.go::if t.InMaintenance(now) {::if !t.InMaintenance(now) {"
  "internal/scheduler/filter.go::if t.Modality != j.Kind {::if t.Modality == j.Kind {"
  "internal/scheduler/filter.go::if j.Qubits > 0 && j.Qubits > t.Qubits {::if j.Qubits > 0 && j.Qubits < t.Qubits {"
  "internal/scheduler/filter.go::if j.Shots > 0 && t.MaxShots > 0 && j.Shots > t.MaxShots {::if j.Shots > 0 && t.MaxShots > 0 && j.Shots < t.MaxShots {"
)

killed=0
survived=0
nonviable=0
survivors=()

restore() { for f in "$@"; do [ -f "$f.mutbak" ] && mv "$f.mutbak" "$f"; done; }
# Always restore every touched file on any exit.
trap 'restore internal/job/phase.go internal/scheduler/filter.go' EXIT

apply() { # file old new -> 0 applied, 3 not found
  python3 - "$1" "$2" "$3" <<'PY'
import sys
f, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
s = open(f).read()
if s.count(old) != 1:
    sys.exit(3)
open(f, 'w').write(s.replace(old, new, 1))
PY
}

echo "Mutation testing ${PKGS[*]} (floor ${FLOOR}%)"
for m in "${MUTANTS[@]}"; do
  file="${m%%::*}"; rest="${m#*::}"; old="${rest%%::*}"; new="${rest##*::}"
  cp "$file" "$file.mutbak"
  if ! apply "$file" "$old" "$new"; then
    echo "  SKIP (not found / not unique): $file :: $old"
    restore "$file"; nonviable=$((nonviable + 1)); continue
  fi
  if ! go build "${PKGS[@]}" >/dev/null 2>&1; then
    echo "  SKIP (does not compile): $file :: $old -> $new"
    restore "$file"; nonviable=$((nonviable + 1)); continue
  fi
  if go test "${PKGS[@]}" >/dev/null 2>&1; then
    echo "  SURVIVED: $file :: $old -> $new"
    survived=$((survived + 1)); survivors+=("$file :: $old -> $new")
  else
    echo "  killed:   $file :: $old -> $new"
    killed=$((killed + 1))
  fi
  restore "$file"
done

viable=$((killed + survived))
echo
echo "mutants: $viable viable ($killed killed, $survived survived), $nonviable non-viable/skipped"
if [ "$viable" -eq 0 ]; then
  echo "FAIL: no viable mutants ran"; exit 1
fi
efficacy=$((100 * killed / viable))
echo "test efficacy: ${efficacy}% (floor ${FLOOR}%)"
if [ "${#survivors[@]}" -gt 0 ]; then
  printf '  survivor: %s\n' "${survivors[@]}"
fi
if [ "$efficacy" -lt "$FLOOR" ]; then
  echo "FAIL: mutation efficacy ${efficacy}% is below floor ${FLOOR}%"; exit 1
fi
echo "PASS"
