#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
#
# Run the hybrid pipeline locally (pre -> quantum -> post) against a Rabi
# endpoint. This is what the Kubernetes Job and the Slurm batch script each wrap
# with their own orchestrator. Set RABI_SERVER and RABI_TOKEN first, e.g.:
#
#   RABI_SERVER=localhost:9090 RABI_TOKEN=dev-key ./run.sh
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export WORK="${WORK:-$(mktemp -d)}"
echo "hybrid pipeline: WORK=$WORK RABI_SERVER=${RABI_SERVER:-unset}"

sh "$DIR/pipeline/pre.sh"
sh "$DIR/pipeline/quantum.sh"
sh "$DIR/pipeline/post.sh"

echo "hybrid pipeline: done"
