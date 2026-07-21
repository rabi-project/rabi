#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# The hybrid pipeline as a Slurm batch job. The two CLASSICAL stages run as
# native Slurm steps on the cluster's CPU/GPU nodes; the QUANTUM stage delegates
# to Rabi via qctl. Submit with:
#
#   export RABI_SERVER=rabi.example.org:9090 RABI_TOKEN=...
#   sbatch --export=ALL,PIPELINE_DIR=$PWD/pipeline pipeline.sbatch
#
#SBATCH --job-name=hybrid-bell
#SBATCH --output=hybrid-%j.out
#SBATCH --ntasks=1
#SBATCH --time=00:05:00
set -euo pipefail

export WORK="${WORK:-$(mktemp -d)}"
PIPELINE_DIR="${PIPELINE_DIR:-$(cd "$(dirname "$0")/pipeline" && pwd)}"
echo "hybrid pipeline (slurm): WORK=$WORK RABI_SERVER=${RABI_SERVER:-unset}"

# Stage 1 — classical (native compute step).
srun sh "$PIPELINE_DIR/pre.sh"
# Stage 2 — quantum (delegated to Rabi).
srun sh "$PIPELINE_DIR/quantum.sh"
# Stage 3 — classical (native compute step).
srun sh "$PIPELINE_DIR/post.sh"

echo "hybrid pipeline (slurm): complete"
