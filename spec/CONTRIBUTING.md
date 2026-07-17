# Contributing

- **Normative changes** (anything in `spec/`, `schemas/`, `proto/`, `conformance/`) require an
  RFC: copy `rfcs/0000-template.md` to `rfcs/NNNN-short-title.md` and open a PR. Discussion on
  the PR; maintainers merge by lazy consensus.
- **Editorial changes** (typos, clarifications that change no behavior) are plain PRs.
- Every commit is signed off (`git commit -s`, DCO). No CLA, by design — see GOVERNANCE.md.
- Proto style: buf lint (DEFAULT); breaking-change check against the last tagged release.
- A normative change without a conformance-test change is incomplete by definition.
