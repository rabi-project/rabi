#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Builds the offline install bundle (M4): every image the chart references
# saved to one archive, the packaged chart, and the install script. Nothing
# in the bundle needs internet at install or run time (air-gap rule,
# phase1-build-plan.md §2).
#
# Usage: deploy/airgap/build-bundle.sh [tag]   (default: airgap)
set -euo pipefail
cd "$(dirname "$0")/../.."

TAG="${1:-airgap}"
OUT="bin/rabi-airgap-$TAG"
rm -rf "$OUT" && mkdir -p "$OUT"

echo "--- images"
docker build -t "rabi:$TAG" . >/dev/null
docker build -t "rabi-adapter-aer:$TAG" -f adapters/aer/Dockerfile . >/dev/null
docker pull -q postgres:15-alpine >/dev/null
# Single-platform save: with the containerd image store a plain save
# carries the multi-arch index whose foreign blobs are absent, and the
# air-gapped side's ctr import fails on the missing digests.
ARCH="linux/$(docker version --format '{{.Server.Arch}}')"
docker save --platform "$ARCH" -o "$OUT/images.tar" "rabi:$TAG" "rabi-adapter-aer:$TAG" postgres:15-alpine

echo "--- chart"
helm package deploy/helm/rabi -d "$OUT" >/dev/null

cp deploy/airgap/install.sh "$OUT/install.sh"
chmod +x "$OUT/install.sh"
echo "$TAG" > "$OUT/TAG"

tar -C bin -czf "$OUT.tgz" "$(basename "$OUT")"
echo "bundle: $OUT.tgz"
