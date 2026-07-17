#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# T6.det (reduced): run a small benchmark twice; every CSV must be
# byte-identical. Runs on PRs touching bench/ or the scheduler.
set -euo pipefail
cd "$(dirname "$0")/.."

run() {
  local out="$1"
  rm -rf "bench/$out"
  (cd bench && uv run python scripts/gen_series.py --hours 24 --out "$out/series.json" >/dev/null)
  go run ./bench/runner --seeds 1 --jobs 40 --series "bench/$out/series.json" \
    --out "bench/$out" >/dev/null
  (cd bench && uv run python scripts/execute.py --out "$out" --workers 2 >/dev/null)
  (cd bench && uv run python scripts/analyze.py --out "$out" --no-charts >/dev/null)
}

run det-a
run det-b

fail=0
for f in series.json physics.csv results.csv summary.csv per_seed.csv effects.csv report.md; do
  if ! cmp -s "bench/det-a/$f" "bench/det-b/$f"; then
    echo "NON-DETERMINISTIC: $f differs between identical runs"
    fail=1
  fi
done
for f in bench/det-a/schedule_*.json; do
  base="$(basename "$f")"
  if ! cmp -s "$f" "bench/det-b/$base"; then
    echo "NON-DETERMINISTIC: $base differs between identical runs"
    fail=1
  fi
done

rm -rf bench/det-a bench/det-b
[ "$fail" -eq 0 ] && echo "BENCH-DETERMINISM OK"
exit $fail
