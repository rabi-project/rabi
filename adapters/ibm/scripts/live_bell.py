# SPDX-License-Identifier: Apache-2.0
"""Nightly live probe (T7.ibm-flag): one Bell job through the IBM adapter
service against a real backend. Requires IBM_TOKEN; asserts SUCCEEDED with
usage records. Open-plan queue times apply — generous timeout.

Usage: IBM_TOKEN=... uv run python scripts/live_bell.py
"""

from __future__ import annotations

import os
import sys
import time
import uuid

from qiskit_ibm_runtime import QiskitRuntimeService

from tangle.adapter.v1alpha1 import adapter_pb2 as pb
from tangle_ibm.service import IBMAdapterService

BELL = b"""
OPENQASM 3.0;
include "stdgates.inc";
qubit[2] q;
bit[2] c;
h q[0];
cx q[0], q[1];
c = measure q;
"""


class Ctx:
    def is_active(self):
        return True

    def abort(self, code, message):
        raise RuntimeError(f"{code}: {message}")


def main() -> int:
    token = os.environ.get("IBM_TOKEN")
    if not token:
        print("IBM_TOKEN not set — live path dormant by design")
        return 0

    runtime = QiskitRuntimeService(channel="ibm_quantum_platform", token=token)
    backend = runtime.least_busy(operational=True, simulator=False)
    print(f"live probe on {backend.name}")
    service = IBMAdapterService({backend.name: backend})

    ctx = Ctx()
    handle = service.SubmitTask(pb.SubmitTaskRequest(
        target=pb.TargetRef(target_id=backend.name),
        idempotency_key=f"nightly-bell-{uuid.uuid4()}",
        payload=pb.Payload(format="openqasm3", inline=BELL),
        shots=1000,
    ), ctx)

    deadline = time.time() + 4 * 3600  # open-plan queues are slow
    while time.time() < deadline:
        st = service.GetTask(pb.TaskRef(target=handle.target, task_id=handle.task_id), ctx)
        if st.state == pb.TaskStatus.State.SUCCEEDED:
            units = {u.unit: u.amount for u in st.usage}
            print(f"SUCCEEDED with usage {units}")
            assert units.get("shots") == 1000
            return 0
        if st.state in (pb.TaskStatus.State.FAILED, pb.TaskStatus.State.CANCELLED):
            print(f"terminal {st.state}: {st.error}")
            return 1
        time.sleep(30)
    print("timed out waiting for the queue")
    return 1


if __name__ == "__main__":
    sys.exit(main())
