<!-- SPDX-License-Identifier: Apache-2.0 -->
# Hybrid workflow: classical + quantum

A runnable pipeline where **classical pre/post stages run on your own scheduler**
(Kubernetes or Slurm) and **the quantum stage is a `QuantumJob` submitted to
Rabi**, with results flowing back to the next classical stage.

> **Rabi schedules the machine that drifts; your scheduler runs everything else.**

This is the canonical answer to *"can Rabi manage CPU/GPU?"* — it doesn't, and it
shouldn't. Kubernetes and Slurm already schedule CPUs and GPUs superbly. A
quantum device is different: it is scarce, shared, and its calibration drifts
between jobs, so *which* device and *when* is a placement decision that needs
live calibration data. That decision is all Rabi owns. Everything else — the
classical compute, the data movement, the DAG — stays with the scheduler you
already run. The boundary is a single step in your pipeline: `qctl submit`.

## The pipeline

Three stages, sharing a working directory:

1. **`pipeline/pre.sh`** — *classical*. Prepares the quantum workload (here: emits
   a Bell-circuit `QuantumJob`; in real pipelines: circuit generation, parameter
   sweeps, transpilation). Touches no quantum resource.
2. **`pipeline/quantum.sh`** — *quantum*. `qctl submit` → wait for a terminal
   state → write the result counts. The only stage that touches a quantum
   resource; Rabi picks the device and records usage.
3. **`pipeline/post.sh`** — *classical*. Verifies the Bell correlation
   (fraction of shots in `|00>`/`|11>`) and asserts entanglement.

Needs `qctl` and `jq` on `PATH`, and `RABI_SERVER` / `RABI_TOKEN` set.

## Run it locally

```bash
# Against a running Rabi (e.g. the compose stack: make compose-up)
RABI_SERVER=localhost:9090 RABI_TOKEN=dev-key ./run.sh
```

## Kubernetes variant (plain Jobs)

`kubernetes/pipeline.yaml` is a `batch/v1` Job: `pre` and `quantum` are init
containers, `post` is the main container, a shared `emptyDir` carries the job
document and results. `kubernetes/rabi-stack.yaml` stands up a self-contained
Rabi (Postgres + Aer replay adapter + rabi) so the example runs in `kind`; in
production the quantum stage just points `RABI_SERVER` at your shared Rabi
endpoint and you deploy none of it.

```bash
# images: rabi:local, rabi-adapter-aer:local, rabi-hybrid-runner:local (kind load)
kubectl apply -f kubernetes/rabi-stack.yaml
kubectl wait --for=condition=available deploy/rabi --timeout=180s
kubectl apply -f kubernetes/pipeline.yaml
kubectl wait --for=condition=complete job/hybrid-bell-pipeline --timeout=180s
```

## Slurm variant (batch)

`slurm/pipeline.sbatch` runs the three stages as native Slurm steps; the quantum
step calls out to Rabi.

```bash
export RABI_SERVER=rabi.example.org:9090 RABI_TOKEN=...
sbatch --export=ALL,PIPELINE_DIR=$PWD/pipeline slurm/pipeline.sbatch
```

## Verified in CI

Both variants (plus the local pipeline against the compose stack) run end to end
in `.github/workflows/hybrid.yml`: `kind` for Kubernetes, a single-node Slurm for
batch. A green run means the classical stages executed on the host scheduler and
the quantum stage's Bell state came back entangled.
