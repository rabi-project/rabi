#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Render the rabi Homebrew formula for a release tag. Usage: render-formula.sh vX.Y.Z
set -euo pipefail
VERSION="$1"
BASE="https://github.com/rabi-project/rabi/releases/download/${VERSION}"
sums="$(curl -fsSL "${BASE}/SHA256SUMS")"
sha() { echo "$sums" | grep " rabi-$1$" | awk '{print $1}'; }
tmpl="$(dirname "$0")/rabi.rb.tmpl"
sed -e "s|__VERSION__|${VERSION#v}|" \
    -e "s|__BASE__|${BASE}|g" \
    -e "s|__SHA_DARWIN_ARM64__|$(sha darwin-arm64)|" \
    -e "s|__SHA_DARWIN_AMD64__|$(sha darwin-amd64)|" \
    -e "s|__SHA_LINUX_ARM64__|$(sha linux-arm64)|" \
    -e "s|__SHA_LINUX_AMD64__|$(sha linux-amd64)|" \
    "$tmpl"
