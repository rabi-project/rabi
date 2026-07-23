# Security checklist (pilot sites)

## Secrets handling
- No password storage exists anywhere (hard constraint; schema-tested —
  `TestNoPasswordColumnsExist`). Authentication is OIDC + hashed API tokens.
- `RABI_BOOTSTRAP_TOKEN` is a first-admin/dev credential: rotate it away
  after minting real tokens (`rabi token create`), then unset it.
- API tokens are stored as SHA-256 digests only (DB-scan-tested). Treat
  the one-time plaintext like any secret; revoke via `rabi token revoke`.
- Vendor credentials (IBM/IQM/QRMI) live only in adapter process
  environments, never in the control plane, repo, or fixtures.
- Postgres credentials: use a dedicated database; the app serves under
  the `rabi_app` role (no UPDATE/DELETE on ledger/audit tables).

## Network matrix
| From | To | Port | Purpose |
|---|---|---|---|
| users / console | rabi | 8080 (HTTP), 9090 (gRPC) | API + console (`/console/`) |
| rabi | Postgres | 5432 | only datastore |
| rabi | adapters | 5005x (gRPC) | adapter protocol |
| adapters | vendor clouds | 443 | only cloud adapters, egress-allowlist per vendor |
| Prometheus | rabi | 8080 `/metrics` | tenant-blind aggregates |

Everything else can be denied. `/healthz` and `/metrics` are unauthenticated
by design (liveness + aggregates); keep them inside the trust boundary or
front them. Air-gapped sites: the offline bundle needs zero egress
(verified by `hack/airgap-verify.sh`).

## Release integrity
- Release CI gates on CVE scan: govulncheck (Go) + trivy (images/fs) with
  no HIGH/CRITICAL findings (`.github/workflows/release.yml`).
- Conformance reports are ed25519-signed; verify against the published key.
- Tags are signed; verify `git tag -v` before deploying.

## Operational
- Audit log (`audit_log`) is DB-grant append-only: every denied call and
  every admin action. Ship it to your SIEM by reading, not by trusting a
  mutable copy.
- Backups: `pg_dump` + `pg_dumpall --roles-only` (docs/backup-restore.md);
  test restore quarterly at minimum — the drill script is CI-run weekly.
