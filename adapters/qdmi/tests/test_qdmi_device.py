# SPDX-License-Identifier: Apache-2.0
"""QDMI binding smoke against the compiled mock device — the ctypes path
itself is what gets exercised; certification is the conformance harness."""

import subprocess
import sys
from pathlib import Path

import pytest
from rabi_qdmi.device import QdmiDevice


@pytest.fixture(scope="session")
def mock_lib(tmp_path_factory) -> str:
    src = Path(__file__).parent.parent / "mock" / "mock_device.c"
    out = tmp_path_factory.mktemp("qdmi") / (
        "libmockqdmi.dylib" if sys.platform == "darwin" else "libmockqdmi.so"
    )
    subprocess.run(["cc", "-shared", "-fPIC", "-o", str(out), str(src)], check=True)
    return str(out)


def test_describe_and_lifecycle(mock_lib):
    dev = QdmiDevice(mock_lib)
    d = dev.describe()
    assert d["resource_id"] == "mock-qdmi-device"
    assert d["num_qubits"] == 7
    assert d["cloud"] is False
    names = {m["name"] for m in d["metrics"]}
    assert "gate.2q.cz.error" in names and "readout.error" in names
    assert all(m["methodology"] == "qdmi-relayed" for m in d["metrics"])
    assert all(m["upstream"] == "mock-1.0.0" for m in d["metrics"])

    task = dev.start("OPENQASM 3.0;\nqubit[2] q;\n", 100)
    state, _ = dev.status(task)
    assert state == "SUCCEEDED"
    assert dev.result(task) == {"00": 50, "11": 50}
    dev.close()


def test_bad_submit_and_missing_symbols(mock_lib, tmp_path):
    dev = QdmiDevice(mock_lib)
    with pytest.raises(RuntimeError):
        dev.start("not qasm at all", 100)  # mock rejects non-OPENQASM
    state, msg = dev.status("never-existed")
    assert state == "FAILED" and "unknown" in msg
    dev.close()

    empty = tmp_path / ("empty.dylib" if sys.platform == "darwin" else "empty.so")
    src = tmp_path / "empty.c"
    src.write_text("int nothing_here(void) { return 0; }\n")
    subprocess.run(["cc", "-shared", "-fPIC", "-o", str(empty), str(src)], check=True)
    with pytest.raises(RuntimeError, match="lacks QDMI device symbols"):
        QdmiDevice(str(empty))
