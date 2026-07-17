# Calibration snapshot data

Versioned device calibration baselines used by the replay fleet and the
benchmark. **Do not edit by hand** — regenerate with:

```sh
cd adapters/aer && uv run --extra dev python scripts/extract_snapshots.py
```

## Provenance

Each `fake_*_20q.json` is extracted from a `qiskit-ibm-runtime` fake backend,
which ships **real historical calibration data** for the corresponding IBM
device, redistributed offline by the package. Because Aer cannot simulate
127+ qubits with noise, each file covers a **connected 20-qubit BFS subgraph**
of the real device (physical qubit indices recorded in `provenance`), with
the vendor-reported T1/T2, readout error, 1q (sx) error, and native 2q gate
error (cz or ecr) for those qubits, remapped to logical indices 0..19.

The exact package version used for extraction is embedded in each file
(`provenance.package_version`).

## Drift is synthetic

Fake backends provide one snapshot per device. The replay fleet synthesizes a
time series: a seeded, strictly-degrading random walk on error metrics
(bounded at +30% relative to baseline; T1/T2 degrade inversely) with a
sawtooth reset at simulated calibration events. This is **synthetic drift
over real calibration baselines**, disclosed as such in the benchmark
methodology (`adapters/aer/src/tangle_aer/replay.py` is the authoritative
implementation).
