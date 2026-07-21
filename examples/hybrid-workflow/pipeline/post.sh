#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Stage 3 — CLASSICAL (runs natively on your scheduler). Post-processes the
# quantum result: computes the Bell correlation (fraction of shots in the
# correlated |00> and |11> outcomes) and asserts the state is entangled. This is
# where real pipelines do error mitigation, aggregation, or feed the next step.
# No quantum resource is touched.
set -eu

WORK="${WORK:-/work}"
THRESHOLD="${THRESHOLD:-0.8}"

# Sum correlated (00, 11) vs total; a clean Bell state puts ~all shots there.
summary=$(jq -r '
  (.["00"] // 0) as $c00 | (.["11"] // 0) as $c11 |
  (.["01"] // 0) as $c01 | (.["10"] // 0) as $c10 |
  ($c00 + $c11) as $corr | ($corr + $c01 + $c10) as $total |
  if $total == 0 then "0 0" else "\($corr) \($total)" end
' "$WORK/result.json")

corr=$(echo "$summary" | cut -d' ' -f1)
total=$(echo "$summary" | cut -d' ' -f2)
if [ "$total" -eq 0 ]; then
  echo "post: no shots in result" >&2
  exit 1
fi

# fraction = corr/total, compared to THRESHOLD without floating-point tools.
frac_x1000=$(( corr * 1000 / total ))
thr_x1000=$(awk "BEGIN{printf \"%d\", $THRESHOLD*1000}")
printf 'post: Bell correlation %d/%d = %d.%d%% (threshold %s)\n' \
  "$corr" "$total" $((frac_x1000 / 10)) $((frac_x1000 % 10)) "$THRESHOLD"

if [ "$frac_x1000" -lt "$thr_x1000" ]; then
  echo "post: FAILED — state is not sufficiently entangled" >&2
  exit 1
fi
echo "post: PASS — entangled Bell state confirmed"
echo "$summary" > "$WORK/summary.txt"
