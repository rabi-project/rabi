# RFC-0001: First-class `technology` and `cloud_queue` fields in the adapter protocol

- **Status:** Accepted (2026-07-18)
- **Author(s):** Edward (Levangie Laboratories)
- **Created:** 2026-07-18
- **Affects:** `proto/tangle/adapter/v1alpha1/adapter.proto`, `spec/overview.md` §2, conformance category 1

## Summary

Promote device technology (superconducting, trapped-ion, neutral-atom, photonic, annealing,
simulator, …) and cloud-queue access mode from `vendor_extensions` string-bag entries to
first-class, conformance-tested fields. `QuantumJob.spec.requirements.technology` and
`backendSelector.allowCloudBurst` already filter on this data; interoperability-defining
data must not live in an extension bag.

## Motivation

The reference implementation (decision D-016a) had to smuggle both values through
`Capabilities.vendor_extensions["technology"]` and `vendor_extensions["cloud"]="true"`
because the protocol offers no field. Consequences observed in practice: the values are
untyped and unvalidated; conformance cannot test honesty for them (category 1 covers only
declared fields); two adapters can disagree on spelling ("trapped-ion" vs "ion-trap") and
silently break fleet filtering. A scheduler whose *primary filter dimensions* ride in an
unspecified side channel is not implementing a standard.

## Design

Additive, wire-compatible change to `tangle.adapter.v1alpha1`:

```proto
message TargetInfo {
  // ...existing fields 1–5...
  // Hardware technology, from the open registry in spec/overview.md §2a.
  // Lowercase kebab-case. Examples: "superconducting", "trapped-ion",
  // "neutral-atom", "photonic", "annealer", "simulator".
  string technology = 6;
}

message Capabilities {
  // ...existing fields 1–11...
  // True when tasks traverse a shared vendor cloud queue outside the site's
  // control (drives backendSelector.allowCloudBurst / preferOnPrem filtering).
  bool cloud_queue = 12;
}
```

Accompanying normative text (`spec/overview.md` §2a, new): an **open technology registry** —
a curated list of canonical strings maintained in the spec repo by PR (not by proto release).
Adapters MUST use a registry value when one applies and MAY use a novel string (which SHOULD
be proposed to the registry). Matching in `requirements.technology` is exact, case-sensitive,
against canonical strings.

**Deprecation window:** control planes MUST read the first-class fields when present and MAY
fall back to `vendor_extensions["technology"]`/`["cloud"]` until spec v0.3, after which the
fallback MUST be removed and the extension keys are reserved (adapters setting only the
extension keys fail conformance from v0.3).

## Compatibility

Proto3-additive: old adapters return empty `technology`/false `cloud_queue`; during the
deprecation window the fallback preserves behavior. No stored-object changes. `TargetInfo`
was chosen over `Capabilities` for `technology` because it is identity-static; `cloud_queue`
sits in `Capabilities` because it describes access mode, which can differ per deployment of
the same device family.

## Alternatives considered

**Keep `vendor_extensions` (status quo).** Rejected: filter-critical data in an untestable
side channel defeats the spec's purpose (see Motivation). **A proto enum.** Rejected:
hardware taxonomy moves faster than wire-format releases; a string + registry gives
canonical values without a proto bump per new modality-technology. **Free-form string, no
registry.** Rejected: reintroduces the spelling-drift problem the RFC exists to kill.

## Conformance impact

Category 1 (capability honesty) gains: `technology` present and ∈ registry (or flagged
novel); `cloud_queue` consistent with observable behavior where testable (a target whose
`unknown_queue` is permanently true and whose vendor is a public cloud SHOULD declare
`cloud_queue=true` — advisory check, warning not failure). From spec v0.3: extension-key
fallback becomes a category-1 failure.

## Unresolved questions

Whether `simulator` (already a bool on `TargetInfo`) should be folded into `technology`
("simulator" as a technology value) and the bool deprecated — proposed for v0.3, kept out of
scope here to stay additive.
