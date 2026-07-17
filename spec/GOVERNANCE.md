# Governance

Tangle's goal hierarchy is explicit: **standard first, company second.** This document is the
binding expression of that decision, published with the first commit so no adopter has to take
it on faith.

## Pre-commitments

1. **License.** The specification, reference implementation, standard adapters, and conformance
   suite are Apache-2.0, permanently. Nothing required to implement, deploy, or interoperate with
   Tangle will ever be relicensed to a source-available or proprietary license.
2. **Nothing shipped open is ever re-closed.** Commercial offerings by the stewarding company
   (support, certified builds, managed operations, integration engineering) are always *new*
   offerings, never the withdrawal of something the community already has.
3. **Donation trigger.** When a second organization ships a serious independent adopter —
   a production deployment, or an independent implementation passing conformance — the
   specification and conformance program will be donated to neutral foundation governance
   (Linux Foundation or equivalent). This is an event-based trigger, not a calendar promise,
   and this file is the public record of it.
4. **Conformance gates naming, never usage.** Anyone may implement the spec freely. The
   "Tangle-conformant" mark (and the Tangle trademark) certify compatibility; they are the only
   things the steward retains, exactly as CNCF retains Kubernetes' marks.

## How decisions are made (bootstrap phase)

- **Maintainers** merge changes. Initial maintainers: the founding team. Standing goal:
  maintainers from **at least two organizations** as early as possible; a second-org maintainer
  is accepted on sustained contribution, not negotiation.
- **RFCs** are required for any normative change (spec text, schemas, protos, conformance
  criteria). RFCs are public PRs; anyone may open one; maintainers decide by lazy consensus,
  recorded on the PR.
- **DCO** sign-off on every commit. No CLA — a CLA would concentrate relicensing power this
  document exists to renounce.

## What "standard first" means when interests conflict

If a choice ever pits the standard's health against the steward company's commercial position,
the standard wins, and this file is the instrument adopters can hold us to.
