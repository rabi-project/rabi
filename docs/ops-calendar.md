<!-- SPDX-License-Identifier: Apache-2.0 -->
# Ops drill calendar

Rabi's reliability claims are backed by drills that run on a schedule, not by
assertion. Each drill is scripted, its result is recorded, and the most recent
game-day is shown on the public status page (`/status`).

| Drill | Cadence | Automation | Result published to |
|---|---|---|---|
| Chaos game-day (8 fault scenarios + invariants) | weekly | `.github/workflows/chaos.yml`; `rabi-chaos` for the supervised `--fleet0` drill | CI report; `game_days` table → status page |
| Load storm (10k jobs / 100 targets) | weekly | `.github/workflows/load.yml` | CI artifact (`storm.json`) |
| Soak (accelerated 72h) | monthly | `.github/workflows/load.yml` | CI artifact (`soak.json`) |
| Upgrade + rollback rehearsal | weekly | `.github/workflows/upgrade.yml` | CI |
| Backup → restore | monthly | `.github/workflows/ops.yml`; `hack/backup-restore-drill.sh` | CI artifact (`drill.json`) |
| Fuzzing (all parsers, ≥1M execs) | weekly | `.github/workflows/security.yml` | CI |
| Mutation testing (scheduler + FSM) | quarterly | `.github/workflows/security.yml` | CI |

## Supervised fleet-0 game-days

Fleet-0 is production, so game-days against it are **scheduled, supervised, and
annotated** — never ad-hoc. The `rabi-chaos` driver requires `--i-mean-it` for
`--target fleet0`, records the drill in the `game_days` table, and the status
page renders the last one ("last game-day date and result"). Destructive
fault-injection against fleet-0 goes through the rehearsed upgrade path
(`docs/fleet0/upgrade.md`), never a manual mutation.

The read-only invariant sweep (`rabi-chaos sweep`) is the safe first game-day: it
verifies the five invariants hold on the live system without injecting a fault.

## Reading a drill result

- **Game-day**: green = all five invariants held after every fault scenario.
- **Backup → restore**: `verified: true` = the restored database's measurement
  tables (jobs, job_events, usage_ledger, tasks) match the source row-for-row.
- **Upgrade**: zero jobs lost across the roll, API unavailability < 30s.

A red drill is loud (non-zero CI exit, artifact shows the failure) and is
triaged before the next release.
