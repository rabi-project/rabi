<!-- SPDX-License-Identifier: Apache-2.0 -->
# Security Policy

Rabi is a control plane for quantum compute fleets. It holds tenant job
documents, usage/billing records, and adapter credentials, so we take
vulnerability reports seriously.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report privately through GitHub's **[Report a vulnerability][advisory]** button
on the repository's Security tab (Security → Advisories → Report a
vulnerability). This opens a private advisory visible only to the maintainers.

If you cannot use GitHub advisories, open a minimal public issue asking a
maintainer to contact you privately — do **not** include details there.

Please include, as far as you can:

- the affected version or commit (`rabi --version`),
- a description of the issue and its impact,
- reproduction steps or a proof of concept,
- any suggested remediation.

## Response commitment

| Stage | Target |
|---|---|
| Acknowledge your report | within **3 business days** |
| Initial triage + severity assessment | within **7 business days** |
| Fix for critical/high severity | targeted within **30 days**, sooner if actively exploited |
| Fix for medium/low severity | scheduled into a normal release |

We will keep you updated through the advisory thread and credit you in the
release notes and the published advisory unless you prefer to remain anonymous.

## Coordinated disclosure

We follow coordinated disclosure. We ask that you give us the response window
above (and reasonable time to ship a fix) before any public disclosure; we aim
to publish an advisory within **90 days** of the report, or sooner once a fix is
released. We will not pursue legal action against good-faith research that
respects this policy and does not harm users or their data.

## Supported versions

Rabi is pre-1.0 (`v0.x`). Security fixes land on `main` and the latest minor
release. Once a `v1.0` LTS line exists (Phase 2 Wave B), this table will list
the supported branches and their support windows.

| Version | Supported |
|---|---|
| latest `v0.x` minor | ✅ |
| older `v0.x` | ❌ (please upgrade) |

## What we already do

- **CVE gate** — every release is blocked on `govulncheck` (reachable-call-graph
  Go advisories) and a Trivy filesystem scan (no HIGH/CRITICAL, unfixed
  excluded).
- **Fuzzing** — every untrusted-input parser (OpenQASM ingestion, admission,
  adapter-result decoding, policy YAML) has a Go fuzz harness run in CI; corpora
  are committed.
- **Supply chain** — releases ship an SPDX SBOM and a signed build-provenance
  attestation alongside `SHA256SUMS`.
- **Secret hygiene** — API and bootstrap tokens are stored only as SHA-256
  digests and compared in constant time; database URLs are redacted before
  logging; a CI test asserts credentials never reach the log stream.
- **Append-only records** — the usage ledger, audit log, and job-event stream
  are append-only at the database-grant level (the serving role cannot
  `UPDATE`/`DELETE`/`TRUNCATE` them).

[advisory]: https://github.com/rabi-project/rabi/security/advisories/new
