# SPDX-License-Identifier: Apache-2.0
"""Offline tests for the IBM adapter using qiskit-ibm-runtime local mode:
fake backends drive the exact SamplerV2 code path the real cloud uses, so
everything except the network is exercised without a token (T7.ibm-flag's
dormant-path counterpart; the live nightly needs IBM_TOKEN)."""

from __future__ import annotations

import json
import time

import pytest
from qiskit_ibm_runtime.fake_provider import FakeManilaV2
from tangle.adapter.v1alpha1 import adapter_pb2 as pb

from tangle_ibm.service import IBMAdapterService, target_metrics

BELL = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
"""


class FakeContext:
    """Minimal grpc context stub."""

    def is_active(self):
        return True

    def abort(self, code, message):
        raise AbortError(code, message)


class AbortError(Exception):
    def __init__(self, code, message):
        super().__init__(f"{code}: {message}")
        self.code = code


@pytest.fixture(scope="module")
def service():
    return IBMAdapterService({"fake-manila": FakeManilaV2()})


def await_terminal(service, handle, timeout=120):
    ctx = FakeContext()
    deadline = time.time() + timeout
    while time.time() < deadline:
        st = service.GetTask(pb.TaskRef(target=handle.target, task_id=handle.task_id), ctx)
        if st.state in (pb.TaskStatus.State.SUCCEEDED, pb.TaskStatus.State.FAILED,
                        pb.TaskStatus.State.CANCELLED):
            return st
        time.sleep(0.2)
    raise TimeoutError("task never terminal")


def test_capabilities_and_state(service):
    ctx = FakeContext()
    caps = service.GetCapabilities(pb.TargetRef(target_id="fake-manila"), ctx)
    assert caps.num_qubits == 5
    assert "openqasm3" in caps.program_formats
    assert caps.vendor_extensions["cloud"] == "true"
    assert caps.cancellation and not caps.sessions

    state = service.GetDeviceState(pb.TargetRef(target_id="fake-manila"), ctx)
    assert state.calibration.snapshot_id.startswith("ibm-")
    assert len(state.calibration.metrics) > 0
    for m in state.calibration.metrics:
        assert m.methodology == "vendor-reported"

    with pytest.raises(AbortError):
        service.GetCapabilities(pb.TargetRef(target_id="nope"), ctx)


def test_metrics_extraction_shapes():
    metrics = target_metrics(FakeManilaV2().target)
    names = {m.name for m in metrics}
    assert "readout.error" in names
    assert any(n.startswith("gate.2q.") for n in names)


def test_bell_end_to_end_local_mode(service):
    ctx = FakeContext()
    handle = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id="fake-manila"),
        idempotency_key="bell-1",
        payload=pb.Payload(format="openqasm3", inline=BELL),
        shots=1000,
    ), ctx)
    # Idempotent resubmission returns the same task.
    again = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id="fake-manila"),
        idempotency_key="bell-1",
        payload=pb.Payload(format="openqasm3", inline=BELL),
        shots=1000,
    ), ctx)
    assert again.task_id == handle.task_id

    st = await_terminal(service, handle)
    assert st.state == pb.TaskStatus.State.SUCCEEDED, st.error
    counts = json.loads(st.result.inline)["counts"]
    shots = sum(counts.values())
    bell = counts.get("00", 0) + counts.get("11", 0)
    assert shots == 1000 and bell / shots > 0.8
    units = {u.unit for u in st.usage}
    assert "shots" in units


def test_error_taxonomy(service):
    ctx = FakeContext()
    bad = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id="fake-manila"),
        idempotency_key="bad-1",
        payload=pb.Payload(format="openqasm3", inline=b"OPENQASM 3.0; nope;"),
        shots=10,
    ), ctx)
    st = await_terminal(service, bad)
    assert st.state == pb.TaskStatus.State.FAILED
    assert st.error.category == pb.ErrorDetail.Category.INVALID_PROGRAM

    wide = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id="fake-manila"),
        idempotency_key="wide-1",
        payload=pb.Payload(format="openqasm3",
                           inline=BELL.replace(b"[2]", b"[9]")),
        shots=10,
    ), ctx)
    st = await_terminal(service, wide)
    assert st.state == pb.TaskStatus.State.FAILED
    assert st.error.category == pb.ErrorDetail.Category.CAPABILITY_MISMATCH

    fmt = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id="fake-manila"),
        idempotency_key="fmt-1",
        payload=pb.Payload(format="qir", inline=BELL),
        shots=10,
    ), ctx)
    st = await_terminal(service, fmt)
    assert st.state == pb.TaskStatus.State.FAILED
    assert st.error.category == pb.ErrorDetail.Category.CAPABILITY_MISMATCH
