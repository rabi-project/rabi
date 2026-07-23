# API guide

Rabi exposes one client API in two equivalent forms: **gRPC** (services in
`tangle.api.v1alpha1`) and a **REST gateway** mapped from it. `rabi` uses
gRPC; anything else (curl, a language SDK, the console) uses REST. Both go
through the same auth and the same handlers.

## Authentication

Every call carries `Authorization: Bearer <credential>`, where the credential
is one of:

- an **OIDC ID token** (JWT) — from `rabi login` or your IdP's flow;
- a **per-project API token** (`rabi_<id>_<secret>`) — from `rabi token create`;
- the **bootstrap token** — dev/first-admin only.

Authorization is role-based (`viewer` < `member` < `operator` < `admin`) and,
for tokens, scoped to a project. Every denied call and every admin action is
written to an append-only audit log. There is no unauthenticated path except
`/healthz` and `/metrics` (tenant-blind).

```sh
curl -H "Authorization: Bearer $RABI_TOKEN" http://HOST:8080/v1alpha1/targets
```

## REST endpoints

Base path `/v1alpha1`. Bodies and responses are JSON (the QuantumJob document
maps directly).

| Method & path | Does |
|---|---|
| `POST /v1alpha1/jobs` | Submit a job. Body: `{ "quantumJob": {…}, "tenant": "...", "dryRun": false }`. Returns the Job. |
| `GET /v1alpha1/jobs/{job_id}` | Fetch one job (spec + status + placement). |
| `GET /v1alpha1/jobs?tenant=&phaseFilter=&pageSize=&pageToken=` | List jobs. |
| `GET /v1alpha1/jobs/{job_id}/watch` | Server-streaming: every transition in order until terminal. |
| `POST /v1alpha1/jobs/{job_id}/cancel` | Request cancellation. |
| `GET /v1alpha1/targets?modalityFilter=` | List fleet targets with capabilities + live calibration. |
| `GET /v1alpha1/targets/{name}` | One target (name is `<site>/<id>`). |
| `GET /v1alpha1/usage?tenant=&from=&to=` | Native-unit usage per target. |

Administration (tokens, projects, quotas, ledger export) is served over gRPC
only, on `rabi.admin.v1alpha1.AdminService` — use `rabi` for those; it is
deliberately not part of the vendored wire spec.

## Submitting a job over REST

The `quantumJob` field is the same document you would pass to `rabi submit`,
as JSON. A program is base64 in `inline`:

```sh
curl -X POST http://HOST:8080/v1alpha1/jobs \
  -H "Authorization: Bearer $RABI_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "tenant": "acme/qa",
    "quantumJob": {
      "apiVersion": "tangle.dev/v1alpha1",
      "kind": "QuantumJob",
      "metadata": { "name": "bell", "tenant": "acme/qa" },
      "spec": {
        "workload": { "kind": "gate-model", "gateModel": {
          "program": { "format": "openqasm3", "inline": "<base64>" },
          "shots": 1000 } }
      }
    }
  }'
```

The response is the Job with its assigned `jobId` and initial `status`. Poll
`GET /v1alpha1/jobs/{jobId}` or stream `.../watch` until `status.phase` is
terminal, then read `status.tasks[].result` and `status.placement`.

## Watching a job

`GET /v1alpha1/jobs/{id}/watch` is a streaming response — each chunk is a Job
snapshot at a new phase, replayed from an append-only event history so no
transition is missed, closing when the job is terminal. `rabi watch` wraps
this; over raw HTTP, read the response as a stream of JSON objects.

## Field naming

REST uses the proto JSON convention: `camelCase` fields (`jobId`,
`boundTarget`, `quantumJob`). The QuantumJob document *inside* keeps its own
spec casing (`gateModel`, `twoQubitErrorMax`) — that is the vendored schema,
unchanged. See the [QuantumJob reference](quantumjob-reference.md) for every
field.

## Wire contracts (for SDK authors)

The normative definitions are the vendored protos and schemas:

- `spec/proto/tangle/api/v1alpha1/` — the client services.
- `spec/proto/tangle/adapter/v1alpha1/` — the adapter protocol (write a driver:
  [conformance-authors.md](conformance-authors.md)).
- `spec/schemas/quantumjob.schema.json` — the QuantumJob document schema.
- `api-config.yaml` — the REST↔gRPC route mapping.

Generate a client in any language from the protos; the REST paths above are
derived from `api-config.yaml` and stable within `v1alpha1`.
