# qctl reference

`qctl` is the Rabi command-line client. Every command is a thin call to the
public API with your bearer credential — anything qctl does, the
[REST API](api-guide.md) can do.

## Global flags & configuration

| flag | env | default | meaning |
|---|---|---|---|
| `--server` | `RABI_SERVER` | `localhost:9090` | control-plane gRPC address |
| `--token` | `RABI_TOKEN` | — | bearer credential (API token, OIDC JWT, or bootstrap token) |
| `--output`, `-o` | — | `table` | `table` or `json` |

Credential precedence: `--token` / `RABI_TOKEN`, then credentials saved by
`qctl login`. Point at a remote deployment with `RABI_SERVER=host:9090`.

```sh
export RABI_SERVER=fleet.example.com:9090
export RABI_TOKEN=rabi_ab12_...            # or run qctl login
qctl targets
```

## Authentication

### `qctl login`
Interactive OIDC login (authorization-code + PKCE). Opens your browser,
stores the resulting credentials for later commands (auto-refreshed).

```sh
qctl login --issuer https://idp.example.com --client-id rabi
```
Flags: `--issuer` (`RABI_OIDC_ISSUER`), `--client-id` (`RABI_OIDC_CLIENT_ID`),
`--no-browser` (print the URL instead of opening it).

### `qctl whoami`
Show the principal your current credential resolves to — subject, name, type
(oidc / token / bootstrap), role, project. The "did my login work" command.

## Jobs

### `qctl submit -f <job.yaml>`
Submit a QuantumJob (YAML or JSON). Prints the job id.
- `--dry-run` — validate only; nothing is enqueued.
```sh
id=$(qctl submit -f bell.yaml | cut -f1)
```

### `qctl get <job-id>`
Full job document: spec, status, placement audit, counts. `-o json` for the
raw object, `-o table` (default) for a readable YAML rendering.

### `qctl list`
List jobs you can see.
- `--tenant <org/project>` — filter to one project (scoped tokens are limited
  to their own regardless).
- `--phase <PHASE>` — filter by lifecycle phase.

### `qctl watch [job-id]`
Stream job transitions live, in order, until terminal.
- with a job id — watch that one job.
- `--all` — watch the whole fleet's activity (the demo view).

### `qctl cancel <job-id>`
Request cancellation. Best-effort cancels the in-flight adapter task, then
transitions the job to CANCELLED (terminal).

## Fleet & usage

### `qctl targets`
List fleet targets with live calibration state: technology, qubits, online
status, cloud-queue flag. `-o json` includes full capabilities and the
calibration snapshot (metrics, methodology, measured-at).

### `qctl usage --tenant <org/project>`
Native-unit usage per target for a project. Native units only — pricing is an
accounting policy, not an API guarantee.

### `qctl usage export --policy <policy.yaml>`
Normalize the immutable usage ledger into cost records (CSV) under a versioned
site policy. Same ledger + same policy version → byte-identical output.
- `--project <org/project>` — scope the export (empty = all you can see).

## Administration

These require operator/admin role (see [concepts § tenancy](concepts.md#tenancy-orgs-projects-quotas)).

### `qctl token …` — per-project API tokens
```sh
qctl token create ci-bot --project acme/qa --role member   # plaintext shown ONCE
qctl token list [--project acme/qa]                         # metadata only, never plaintext
qctl token revoke <id>                                      # immediate; rotation = create + revoke
```
Roles: `viewer` < `member` < `operator` < `admin`. Tokens are stored only as
hashes.

### `qctl project …` — org/project lifecycle
```sh
qctl project create acme/qa [--weight 3]     # fair-share weight
qctl project list [--all]                    # --all includes archived
qctl project archive acme/qa                 # stops new submissions; history kept
```

### `qctl quota …` — per-project native-unit limits
```sh
qctl quota set acme/qa shots 20000
qctl quota set acme/qa shots --remove
qctl quota list [--project acme/qa]
```

## Exit status & scripting

Commands exit non-zero on error with a message on stderr. `-o json` output is
stable and pipeable (e.g. `qctl get "$id" -o json | jq .status.phase`). A
typical submit-and-wait loop:

```sh
id=$(qctl submit -f job.yaml | cut -f1)
while :; do
  phase=$(qctl get "$id" -o json | jq -r .status.phase)
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break;; esac
  sleep 2
done
echo "$phase"
```
