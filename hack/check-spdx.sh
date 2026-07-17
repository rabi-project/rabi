#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Every source file carries an SPDX header (mvp-build-plan.md §2). Vendored
# spec/ and generated gen/ are checked by their own provenance, not here.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
while IFS= read -r f; do
  if ! head -3 "$f" | grep -q 'SPDX-License-Identifier: Apache-2.0'; then
    echo "missing SPDX header: $f"
    fail=1
  fi
done < <(git ls-files -- '*.go' '*.py' '*.sh' '*.sql' '*.yml' '*.yaml' 'Dockerfile*' 'Makefile' \
  | grep -v '^spec/' | grep -v '^gen/' | grep -v '^.github/' \
  | grep -v '^adapters/aer/src/tangle/' \
  | grep -v '/testdata/')  # generated stubs + machine-written test fixtures

if [ "$fail" -ne 0 ]; then
  echo "SPDX check failed"
  exit 1
fi
echo "SPDX check OK"
