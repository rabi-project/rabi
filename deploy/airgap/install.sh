#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Installs Rabi from the offline bundle on an air-gapped Kubernetes host.
# Every image comes from images.tar; pullPolicy=Never turns any attempted
# egress into a hard failure instead of a silent download.
#
# Usage: ./install.sh [release-name] [bootstrap-token]
# Env:   KIND_CLUSTER=<name> to load images into a kind cluster instead of
#        the local container runtime.
set -euo pipefail
cd "$(dirname "$0")"

RELEASE="${1:-rabi}"
TOKEN="${2:-change-me-on-first-login}"
TAG="$(cat TAG)"

echo "--- loading images"
if [ -n "${KIND_CLUSTER:-}" ]; then
  kind load image-archive images.tar --name "$KIND_CLUSTER"
else
  docker load -i images.tar
fi

echo "--- helm install"
helm install "$RELEASE" rabi-*.tgz \
  --set image.tag="$TAG" --set image.pullPolicy=Never \
  --set adapterAer.image.tag="$TAG" --set adapterAer.image.pullPolicy=Never \
  --set postgres.pullPolicy=Never \
  --set auth.bootstrapToken="$TOKEN" \
  --wait --timeout 5m
