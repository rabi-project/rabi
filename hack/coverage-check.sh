#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Coverage floors (test-and-verification-plan.md §2): scheduler & state
# machine ≥ 90%, store ≥ 85%, hand-written control-plane code overall ≥ 75%.
# Floors may rise, never fall. Generated code (gen/), the vendored spec, and
# binary entrypoints (cmd/) are out of scope (docs/decisions.md D-012).
#
# Statement coverage is aggregated per package straight from the profile:
# each line is "<file>:<pos> <numStatements> <hitCount>".
set -euo pipefail
cd "$(dirname "$0")/.."

profile="bin/coverage.out"
mkdir -p bin
# -p 1: coverage-instrumented packages each start their own Postgres
# testcontainer; running them in parallel OOM-killed a container on 7GB CI
# runners. One package (and one container) at a time.
if ! go test -p 1 -count=1 -coverprofile="$profile" -coverpkg=./internal/... ./internal/... > bin/coverage-test.log 2>&1; then
  echo "coverage test run FAILED:"
  cat bin/coverage-test.log
  exit 1
fi

pkg_pct() { # prefix ("" = all)
  # Blocks appear once per test binary; dedupe by block key, max hit count.
  awk -v prefix="$1" 'NR > 1 {
    file=$1; sub(/:.*/, "", file)
    dir=file; sub(/\/[^\/]*$/, "", dir)
    if (prefix != "" && dir != prefix) next
    key=$1
    stmts[key] = $2
    if ($3 > hits[key]) hits[key] = $3
  } END {
    for (key in stmts) {
      total += stmts[key]
      if (hits[key] > 0) covered += stmts[key]
    }
    if (total) printf "%.1f", covered * 100 / total; else print "0.0"
  }' "$profile"
}

fail=0
check() { # name pct floor
  printf "%-45s %6s%%  (floor %s%%)\n" "$1" "$2" "$3"
  if [ "$(echo "$2 >= $3" | bc -l)" != "1" ]; then
    echo "FAIL: $1 coverage $2% is below floor $3%"
    fail=1
  fi
}

check "internal (total)" "$(pkg_pct "")" 75
check "internal/job (fsm + validation)" "$(pkg_pct github.com/mAengo31/rabi/internal/job)" 90
check "internal/store" "$(pkg_pct github.com/mAengo31/rabi/internal/store)" 85
if [ -d internal/scheduler ]; then
  check "internal/scheduler" "$(pkg_pct github.com/mAengo31/rabi/internal/scheduler)" 90
fi

exit $fail
