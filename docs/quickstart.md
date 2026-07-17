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

Tear down with `make compose-down`.

Simulated QPU targets, job submission, and the live demo arrive in later
milestones; this page grows with them.
