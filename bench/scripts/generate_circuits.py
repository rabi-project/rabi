# SPDX-License-Identifier: Apache-2.0
"""Vendor the benchmark circuit subset from MQT Bench.

Generates a fixed set of algorithm circuits (widths 2-20), decomposes them to
a flat rz/sx/x/cx basis (deterministic transpile, seed 7), and writes
OpenQASM 3 into bench/circuits/ with attribution headers. Committed output is
the versioned artifact; regeneration requires `uv sync --extra gen`.

Attribution: MQT Bench (Quetschlich, Burgholzer, Wille — "MQT Bench:
Benchmarking Software and Design Automation Tools for Quantum Computing",
Quantum 7, 1062 (2023)), MIT license.

Usage: uv run --extra gen python scripts/generate_circuits.py
"""

from __future__ import annotations

import importlib.metadata
import math
from pathlib import Path

from mqt.bench import BenchmarkLevel, get_benchmark
from qiskit import qasm3, transpile

OUT = Path(__file__).parent.parent / "circuits"
SEED = 7

# (benchmark, widths): mixed 2-20 qubits per mvp-build-plan.md §M6.
#
# Subset restricted to circuits with analytically CONCENTRATED ideal outputs
# (GHZ: 2 outcomes; DJ/BV/QPE-exact: ~1; W-state: n; Grover: marked states).
# Flat-output families (qft, graphstate, qaoa on |0..0>) are excluded because
# sampling cannot distinguish noisy from ideal for them at finite shots —
# any fidelity-from-counts metric reads noise-blind there. Documented in the
# benchmark report methodology.
SUBSET = [
    ("ghz", [2, 4, 8, 12, 16, 20]),
    ("dj", [3, 6, 10, 14]),
    ("wstate", [3, 6, 10, 14, 18]),
    ("bv", [4, 8, 12, 16, 20]),
    ("qpeexact", [4, 8, 12]),  # 16q QPE is too deep to noise-simulate in budget
    ("grover", [3, 5, 7]),
]


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    version = importlib.metadata.version("mqt.bench")
    count = 0
    for name, widths in SUBSET:
        for width in widths:
            qc = get_benchmark(name, BenchmarkLevel.INDEP, circuit_size=width,
                               random_parameters=False)
            if qc.parameters:  # bind deterministically: p_i = (i+1)/(n+1) · π
                n = len(qc.parameters)
                qc = qc.assign_parameters([math.pi * (i + 1) / (n + 1) for i in range(n)])
            if not any(inst.operation.name == "measure" for inst in qc.data):
                qc.measure_all()
            flat = transpile(qc, basis_gates=["rz", "sx", "x", "cx"],
                             optimization_level=1, seed_transpiler=SEED)
            flat.name = f"{name}_{width}"
            path = OUT / f"{name}_{width:02d}.qasm"
            header = (
                f"// SPDX-License-Identifier: MIT\n"
                f"// Source: MQT Bench v{version} — benchmark '{name}', {width} qubits,\n"
                f"// level INDEP, random_parameters=False; decomposed to rz/sx/x/cx\n"
                f"// (optimization_level=1, seed_transpiler={SEED}) by\n"
                f"// bench/scripts/generate_circuits.py. MQT Bench: Quetschlich,\n"
                f"// Burgholzer, Wille, Quantum 7, 1062 (2023). MIT license.\n"
            )
            path.write_text(header + qasm3.dumps(flat) + "\n")
            count += 1
    print(f"wrote {count} circuits to {OUT}")


if __name__ == "__main__":
    main()
