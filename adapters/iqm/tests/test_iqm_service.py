# SPDX-License-Identifier: Apache-2.0
"""Cassette-mode smoke; certification is the conformance harness."""

import time

from rabi_iqm.backends import CassetteIqm
from rabi_iqm.service import IqmAdapterService

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
    def abort(self, code, msg):  # pragma: no cover
        raise AssertionError(f"abort: {code} {msg}")

    def is_active(self):
        return True


def test_capabilities_and_lifecycle():
    svc = IqmAdapterService({"cassette-iqm": CassetteIqm()})
    caps = svc.GetCapabilities(pb.TargetRef(target_id="cassette-iqm"), _Ctx())
    assert caps.target.vendor == "iqm"
    assert caps.cloud_queue is True
    assert caps.target.technology == "superconducting"

    h = svc.SubmitTask(
        pb.SubmitTaskRequest(
            target=pb.TargetRef(target_id="cassette-iqm"),
            idempotency_key="iqm-1",
            payload=pb.Payload(format="openqasm3", inline=BELL),
            shots=200,
        ),
        _Ctx(),
    )
    deadline = time.time() + 10
    while time.time() < deadline:
        st = svc.GetTask(
            pb.TaskRef(target=pb.TargetRef(target_id="cassette-iqm"), task_id=h.task_id),
            _Ctx())
        if st.state == pb.TaskStatus.State.SUCCEEDED:
            break
        time.sleep(0.05)
    assert st.state == pb.TaskStatus.State.SUCCEEDED
    assert {u.unit for u in st.usage} == {"shots", "tasks"}
