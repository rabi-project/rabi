# SPDX-License-Identifier: Apache-2.0
"""Unit smoke for the QRMI adapter in cassette mode; full certification is
the conformance harness (hack/conformance-report.sh)."""

import time

import pytest
from rabi_qrmi.backends import CassetteQrmi, _counts_from_primitive_result, _metrics_from_target
from rabi_qrmi.service import QrmiAdapterService

from tangle.adapter.v1alpha1 import adapter_pb2 as pb

BELL = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
"""


class _Ctx:
    def abort(self, code, msg):  # pragma: no cover - only on misuse
        raise AssertionError(f"abort: {code} {msg}")

    def is_active(self):
        return True


@pytest.fixture
def svc() -> QrmiAdapterService:
    return QrmiAdapterService({"cassette-qrmi": CassetteQrmi("cassette-qrmi")})


def _submit(svc, key: str, shots: int = 100, params: dict | None = None):
    return svc.SubmitTask(
        pb.SubmitTaskRequest(
            target=pb.TargetRef(target_id="cassette-qrmi"),
            idempotency_key=key,
            payload=pb.Payload(format="openqasm3", inline=BELL),
            shots=shots,
            parameters=params or {},
        ),
        _Ctx(),
    )


def _await_terminal(svc, handle, timeout=10.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        st = svc.GetTask(
            pb.TaskRef(target=pb.TargetRef(target_id="cassette-qrmi"),
                       task_id=handle.task_id), _Ctx())
        if st.state in (pb.TaskStatus.State.SUCCEEDED, pb.TaskStatus.State.FAILED,
                        pb.TaskStatus.State.CANCELLED):
            return st
        time.sleep(0.05)
    raise AssertionError("task never terminal")


def test_capabilities_and_provenance(svc):
    caps = svc.GetCapabilities(pb.TargetRef(target_id="cassette-qrmi"), _Ctx())
    assert caps.target.technology == "superconducting"
    assert caps.cloud_queue is True
    assert caps.sessions is False
    state = svc.GetDeviceState(pb.TargetRef(target_id="cassette-qrmi"), _Ctx())
    assert state.calibration.snapshot_id
    assert state.calibration.metrics
    for m in state.calibration.metrics:
        assert "qrmi-relayed" in m.methodology


def test_lifecycle_usage_and_idempotency(svc):
    h1 = _submit(svc, "life-1")
    st = _await_terminal(svc, h1)
    assert st.state == pb.TaskStatus.State.SUCCEEDED
    units = {u.unit: u.amount for u in st.usage}
    assert units == {"shots": 100.0, "tasks": 1.0}
    # Same key → same task.
    h2 = _submit(svc, "life-1")
    assert h2.task_id == h1.task_id


def test_invalid_program_and_shots_taxonomy(svc):
    bad = svc.SubmitTask(
        pb.SubmitTaskRequest(
            target=pb.TargetRef(target_id="cassette-qrmi"),
            idempotency_key="bad-1",
            payload=pb.Payload(format="openqasm3",
                               inline=b"OPENQASM 3.0; definitely not a program;"),
            shots=10,
        ),
        _Ctx(),
    )
    st = _await_terminal(svc, bad)
    assert st.state == pb.TaskStatus.State.FAILED
    assert st.error.category == pb.ErrorDetail.Category.INVALID_PROGRAM

    over = _submit(svc, "over-1", shots=1_000_000)
    st = _await_terminal(svc, over)
    assert st.state == pb.TaskStatus.State.FAILED
    assert st.error.category == pb.ErrorDetail.Category.CAPABILITY_MISMATCH


def test_cancel_queued_never_runs(svc):
    slow = _submit(svc, "cancel-slow", params={"rabi.sim/delay-ms": "1500"})
    queued = _submit(svc, "cancel-queued")
    resp = svc.CancelTask(
        pb.TaskRef(target=pb.TargetRef(target_id="cassette-qrmi"),
                   task_id=queued.task_id), _Ctx())
    assert resp.accepted
    st = _await_terminal(svc, queued)
    assert st.state == pb.TaskStatus.State.CANCELLED
    assert not st.usage
    assert _await_terminal(svc, slow).state == pb.TaskStatus.State.SUCCEEDED


def test_target_document_mapping():
    num, metrics = _metrics_from_target(
        '{"num_qubits": 5, "backend_version": "1.2.3", "gates": ['
        '{"gate": "cx", "qubits": [[0, 1]], "parameters": '
        '[{"name": "gate_error", "value": 0.01}]}]}'
    )
    assert num == 5
    assert metrics[0]["name"] == "gate.2q.cx.error"
    assert metrics[0]["upstream"] == "1.2.3"
    assert _metrics_from_target("not json") == (0, [])

    counts = _counts_from_primitive_result(
        {"results": [{"data": {"c": {"counts": {"00": 50, "11": 50}}}}]}
    )
    assert counts == {"00": 50, "11": 50}
    assert _counts_from_primitive_result({}) == {}
