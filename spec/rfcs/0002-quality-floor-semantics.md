# RFC-0002: Quality-floor evaluation semantics (`best`-value aggregate, with override)

- **Status:** Accepted (2026-07-18)
- **Author(s):** Edward (Levangie Laboratories)
- **Created:** 2026-07-18
- **Affects:** `spec/quantumjob.md` (`requirements.quality`), `schemas/quantumjob.schema.json`, conformance (scheduler-side), golden placement suite

## Summary

Ratify what the reference implementation does (decision D-016b) and the spec left open: a
quality floor (e.g. `twoQubitErrorMax`) is evaluated against the device's **best** (minimum-
error) qubit/edge/readout metric in the placement-time calibration snapshot — a device is
feasible when at least one region satisfies the floor. Add an explicit, optional
`aggregate` override (`best | median | worst`) for sites and users who want conservatism,
with `best` as the normative default.

## Motivation

A floor needs an aggregate to be meaningful: a 156-qubit device has hundreds of two-qubit
error values. Three candidate semantics produce materially different fleets. The reference
implementation chose best-value on the argument that transpilation steers circuits toward
the good region, so "at least one satisfying region exists" is the least surprising
feasibility test — and it documents the choice in every rejection string ("best two-qubit
error 0.0071 exceeds floor 0.006"). This RFC makes that behavior normative rather than
implementation-defined, because two conforming schedulers disagreeing on feasibility for the
same job and snapshot would fork the standard's meaning.

## Design

Normative text added to `spec/quantumjob.md`:

1. Each quality-floor key defines its metric selector (e.g. `twoQubitErrorMax` selects
   metrics named `gate.2q.*.error`). Evaluation applies the **aggregate** over all selected
   metrics in the snapshot, then compares against the floor.
2. Default aggregate is **`best`** (minimum for error-type metrics, maximum for
   fidelity-type metrics — "most favorable value").
3. Optional override, per modality block:

```yaml
requirements:
  quality:
    gateModel:
      twoQubitErrorMax: 0.006
      readoutErrorMax: 0.02
      aggregate: best        # best | median | worst; default best
```

4. Placement audit records MUST state the aggregate used and the winning value
   (already true in the reference implementation's rejection strings; now required).
5. `calibrationMaxAge` composes unchanged: stale snapshots exclude the target before any
   aggregate is computed.

Schema change: add `aggregate` enum to each modality quality block (additive, default `best`).

## Compatibility

Default preserves current reference behavior exactly (golden placement suite must not change
for jobs that don't set `aggregate`). Additive schema field; no proto changes; no stored-object
migration.

## Alternatives considered

**`worst`-value as default.** Rejected: rejects feasible placements wholesale (one bad edge
on a 156-qubit device disqualifies it), punishing exactly the large devices the fleet exists
to use; available as an explicit override for conservative sites. **Mean/average.** Rejected
as a default: statistically seductive, operationally meaningless — placements execute on a
region, not on the average. Median is offered instead of mean as the middle option because it
is robust to outlier dead qubits. **Percentile parameter (e.g. `p90`).** Deferred: adds a
numeric knob with unclear demand; `median` covers the intent; revisit on evidence.

## Conformance impact

New scheduler-conformance tests (golden-style): a crafted snapshot with known
best/median/worst values per metric class; one job per aggregate mode asserting
include/exclude and the audit string's aggregate + value. Rejection-string format for floors
becomes normative (aggregate name + winning value + floor).

## Unresolved questions

Whether `aggregate` should also be settable as a per-tenant or per-site default (policy
layer rather than spec) — parked for the tenancy-policy RFC in v0.3.
