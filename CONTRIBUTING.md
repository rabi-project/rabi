# Contributing to Rabi

- **License:** Apache-2.0. Every source file carries an
  `SPDX-License-Identifier: Apache-2.0` header (CI-enforced).
- **DCO:** every commit is signed off (`git commit -s`), certifying the
  [Developer Certificate of Origin](https://developercertificate.org/).
  There is no CLA, by design.
- **The spec is law.** `spec/` is a read-only vendored copy of
  [`rabi-spec`](../rabi-spec). Wire-contract changes happen there via RFC,
  never here.
- **Milestone discipline:** work lands milestone by milestone per
  `spec/mvp-build-plan.md`; a milestone PR without its test suites green
  (`spec/test-and-verification-plan.md` §2) is incomplete by definition.
- **Goldens are code:** changing a golden placement file requires the
  `golden-change` PR label and a one-line justification per changed scenario.
- No copyleft dependencies.
