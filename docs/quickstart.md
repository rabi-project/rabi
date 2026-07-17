# Quickstart

From clone to a running (for now, empty) fleet in 4 commands. Requires Docker
with Compose and Go ≥1.22.

```sh
git clone https://github.com/tangle-dev/tangle.git
cd tangle
make compose-up          # builds tangled, starts Postgres + control plane
go run ./cmd/qctl targets --api-key dev-key
```

Expected output (the compose fleet ships one simulated Aer QPU at M2):

```
NAME           MODALITY    QUBITS  STATUS
sim/aer-alpha  gate-model  5       ONLINE
```

The same answer over REST:

```sh
curl -H "Authorization: Bearer dev-key" http://localhost:8080/v1alpha1/targets
```

## Run a job

```sh
export TANGLE_API_KEY=dev-key
go run ./cmd/qctl submit -f examples/bell.yaml          # prints "<job-id>  PENDING"
go run ./cmd/qctl watch <job-id>                        # PENDING → ... → SUCCEEDED
go run ./cmd/qctl get <job-id>                          # counts + placement audit
go run ./cmd/qctl usage --tenant demo                   # native-unit usage ledger
```

The Bell job routes to the simulated QPU, executes under its calibration
snapshot's noise model, and returns a counts histogram with |00⟩ and |11⟩
dominant. `status.placement` records the policy, calibration snapshot id, and
a human-readable reason for the binding. Jobs are validated against the
spec's JSON Schema at admission — try breaking `examples/bell.yaml` and the
error names the exact field.

Tear down with `make compose-down`.
