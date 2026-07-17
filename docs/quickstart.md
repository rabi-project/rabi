# Quickstart

From clone to a running (for now, empty) fleet in 4 commands. Requires Docker
with Compose and Go ≥1.22.

```sh
git clone https://github.com/tangle-dev/tangle.git
cd tangle
make compose-up          # builds tangled, starts Postgres + control plane
go run ./cmd/qctl targets --api-key dev-key
```

Expected output at M0:

```
0 targets
```

The same answer over REST:

```sh
curl -H "Authorization: Bearer dev-key" http://localhost:8080/v1alpha1/targets
```

## Submit a job (M1)

```sh
export TANGLE_API_KEY=dev-key
go run ./cmd/qctl submit -f examples/bell.yaml          # prints "<job-id>  PENDING"
go run ./cmd/qctl get <job-id>                          # full document + status
go run ./cmd/qctl watch <job-id>                        # streams phase transitions
go run ./cmd/qctl cancel <job-id>                       # PENDING → CANCELLED
```

Jobs are validated against the spec's JSON Schema at admission — try breaking
`examples/bell.yaml` and the error names the exact field. With no adapters
registered yet the job carries a `FormatAvailable: False` condition and waits
in `PENDING`; simulated QPU targets arrive in M2.

Tear down with `make compose-down`.
