# rabi reference

`rabi` is the Rabi command-line client. Every command is a thin call to the
public API with your bearer credential — anything rabi does, the
[REST API](api-guide.md) can do.

## Global flags & configuration

| flag | env | default | meaning |
|---|---|---|---|
| `--server` | `RABI_SERVER` | `localhost:9090` | control-plane gRPC address |
| `--token` | `RABI_TOKEN` | — | bearer credential (API token, OIDC JWT, or bootstrap token) |
| `--output`, `-o` | — | `table` | `table` or `json` |

Credential precedence: `--token` / `RABI_TOKEN`, then credentials saved by
`rabi login`. Point at a remote deployment with `RABI_SERVER=host:9090`.

```sh
export RABI_SERVER=fleet.example.com:9090
export RABI_TOKEN=rabi_ab12_...            # or run rabi login
rabi targets
```

## Authentication

### `rabi login`
Interactive OIDC login (authorization-code + PKCE). Opens your browser,
stores the resulting credentials for later commands (auto-refreshed).

```sh
rabi login --issuer https://idp.example.com --client-id rabi
```
Flags: `--issuer` (`RABI_OIDC_ISSUER`), `--client-id` (`RABI_OIDC_CLIENT_ID`),
`--no-browser` (print the URL instead of opening it).

### `rabi whoami`
Show the principal your current credential resolves to — subject, name, type
(oidc / token / bootstrap), role, project. The "did my login work" command.

## Jobs

### `rabi submit -f <job.yaml>`
Submit a QuantumJob (YAML or JSON). Prints the job id.
- `--dry-run` — validate only; nothing is enqueued.
```sh
id=$(rabi submit -f bell.yaml | cut -f1)
```

### `rabi get <job-id>`
Full job document: spec, status, placement audit, counts. `-o json` for the
raw object, `-o table` (default) for a readable YAML rendering.

### `rabi list`
List jobs you can see.
- `--tenant <org/project>` — filter to one project (scoped tokens are limited
  to their own regardless).
- `--phase <PHASE>` — filter by lifecycle phase.

### `rabi watch [job-id]`
Stream job transitions live, in order, until terminal.
- with a job id — watch that one job.
- `--all` — watch the whole fleet's activity (the demo view).

### `rabi cancel <job-id>`
Request cancellation. Best-effort cancels the in-flight adapter task, then
transitions the job to CANCELLED (terminal).

## Fleet & usage

### `rabi targets`
List fleet targets with live calibration state: technology, qubits, online
status, cloud-queue flag. `-o json` includes full capabilities and the
calibration snapshot (metrics, methodology, measured-at).

### `rabi usage --tenant <org/project>`
Native-unit usage per target for a project. Native units only — pricing is an
accounting policy, not an API guarantee.

### `rabi usage export --policy <policy.yaml>`
Normalize the immutable usage ledger into cost records (CSV) under a versioned
site policy. Same ledger + same policy version → byte-identical output.
- `--project <org/project>` — scope the export (empty = all you can see).

## Administration

These require operator/admin role (see [concepts § tenancy](concepts.md#tenancy-orgs-projects-quotas)).

### `rabi token …` — per-project API tokens
```sh
rabi token create ci-bot --project acme/qa --role member   # plaintext shown ONCE
rabi token list [--project acme/qa]                         # metadata only, never plaintext
rabi token revoke <id>                                      # immediate; rotation = create + revoke
```
Roles: `viewer` < `member` < `operator` < `admin`. Tokens are stored only as
hashes.

### `rabi project …` — org/project lifecycle
```sh
rabi project create acme/qa [--weight 3]     # fair-share weight
rabi project list [--all]                    # --all includes archived
rabi project archive acme/qa                 # stops new submissions; history kept
```

### `rabi quota …` — per-project native-unit limits
```sh
rabi quota set acme/qa shots 20000
rabi quota set acme/qa shots --remove
rabi quota list [--project acme/qa]
```

## Exit status & scripting

Commands exit non-zero on error with a message on stderr. `-o json` output is
stable and pipeable (e.g. `rabi get "$id" -o json | jq .status.phase`). A
typical submit-and-wait loop:

```sh
id=$(rabi submit -f job.yaml | cut -f1)
while :; do
  phase=$(rabi get "$id" -o json | jq -r .status.phase)
  case "$phase" in SUCCEEDED|FAILED|CANCELLED) break;; esac
  sleep 2
done
echo "$phase"
```
