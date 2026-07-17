# Quickstart

Clone to a routed-jobs view in 5 commands. Requires Docker with Compose and
Go ≥1.22.

```sh
git clone https://github.com/mAengo31/Rabi.git
cd Rabi
make compose-up               # 3 simulated QPUs replaying real IBM calibration + control plane
./deploy/compose/seed.sh      # submit the 20-job demo mix
go run ./cmd/qctl watch --all # live fleet view (Ctrl-C to exit)
```

The fleet replays **real device calibration** (20-qubit subgraphs of IBM's
fake_torino / fake_sherbrooke / fake_brisbane, drifting at 600× wall time),
and the `calib-aware/v0` policy routes each job by live calibration quality.
The watch view shows jobs being filtered, scored, bound, executed, and
accounted — every placement carries a human-readable reason, including
per-target rejections. The seed mix deliberately ends with jobs in every
state: successes across the fleet, `FAILED` with a categorized error
(`INVALID_PROGRAM`), `CANCELLED`, and `PENDING` jobs whose constraints no
device can currently satisfy — with the reason recorded.

Poke at individual jobs:

```sh
export TANGLE_API_KEY=dev-key
go run ./cmd/qctl targets                 # fleet with live calibration state
go run ./cmd/qctl list --tenant demo
go run ./cmd/qctl get <job-id>            # full document, placement audit, counts
go run ./cmd/qctl usage --tenant demo     # native-unit usage ledger
```

The same API over REST:

```sh
curl -H "Authorization: Bearer dev-key" http://localhost:8080/v1alpha1/targets
```

## Optional: a real IBM Quantum backend

Off by default. With an IBM Quantum token (open-plan queue times can be
**hours** — the demo does not wait for it):

```sh
IBM_TOKEN=<token> TANGLE_ADAPTERS_EXTRA=",ibm=adapter-ibm:50052" \
  docker compose -f deploy/compose/docker-compose.yml --profile ibm up -d
```

The IBM target then appears in `qctl targets` (vendor `ibm`, `cloud=true`);
jobs reach it only when `backendSelector.allowCloudBurst` lists it.

## The benchmark (Artifact B)

```sh
make bench    # ~30 min: 3 policies × 5 seeds × 500 jobs; report in bench/out/report.md
```

Tear down with `make compose-down`.
