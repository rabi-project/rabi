# SPDX-License-Identifier: Apache-2.0
"""ctypes binding to a QDMI device library (phase1-build-plan.md M9).

QDMI's contract is a C ABI: a device is a shared library. This binding
targets the QDMI 1.0-shaped device-interface symbols listed in SYMBOLS —
deliberately centralized so that when a real site's library differs, the
fix is this one table plus the site recipe's symbol check
(docs/qdmi-site-recipe.md). CI certifies through a compiled mock device
(mock/mock_device.c) so the ctypes path itself is what gets tested.

The class exposes the shared adapter-chassis interface
(describe/start/status/result/stop), so rabi_qrmi's service machinery
serves it unchanged (docs/decisions.md D-043).
"""

from __future__ import annotations

import ctypes
import threading
import uuid

SYMBOLS = (
    "QDMI_device_initialize",
    "QDMI_device_query_name",
    "QDMI_device_query_version",
    "QDMI_device_query_qubits_num",
    "QDMI_device_query_operation_error",
    "QDMI_device_query_readout_error",
    "QDMI_device_job_submit",
    "QDMI_device_job_status",
    "QDMI_device_job_cancel",
    "QDMI_device_job_result_hist",
    "QDMI_device_job_free",
)

_STATUS = {0: "QUEUED", 1: "RUNNING", 2: "SUCCEEDED", 3: "FAILED", 4: "CANCELLED"}
_TWO_QUBIT_OP = b"cz"


class QdmiDevice:
    """One QDMI device library, loaded via dlopen."""

    def __init__(self, library_path: str):
        self._lib = ctypes.CDLL(library_path)
        missing = [s for s in SYMBOLS if not hasattr(self._lib, s)]
        if missing:
            raise RuntimeError(
                f"{library_path} lacks QDMI device symbols {missing}; "
                "see docs/qdmi-site-recipe.md for the ABI check"
            )
        self._lib.QDMI_device_query_operation_error.argtypes = [
            ctypes.c_char_p, ctypes.c_int, ctypes.c_int, ctypes.POINTER(ctypes.c_double)]
        self._lib.QDMI_device_query_readout_error.argtypes = [
            ctypes.c_int, ctypes.POINTER(ctypes.c_double)]
        self._lib.QDMI_device_job_submit.argtypes = [
            ctypes.c_char_p, ctypes.c_long, ctypes.POINTER(ctypes.c_void_p)]
        self._lock = threading.Lock()
        self._jobs: dict[str, ctypes.c_void_p] = {}
        if self._lib.QDMI_device_initialize() != 0:
            raise RuntimeError(f"{library_path}: QDMI_device_initialize failed")
        self._library_path = library_path

    # -- chassis interface ---------------------------------------------------

    def describe(self) -> dict:
        name = ctypes.create_string_buffer(256)
        version = ctypes.create_string_buffer(256)
        num = ctypes.c_int(0)
        self._check(self._lib.QDMI_device_query_name(name, 256), "query_name")
        self._check(self._lib.QDMI_device_query_version(version, 256), "query_version")
        self._check(self._lib.QDMI_device_query_qubits_num(ctypes.byref(num)), "qubits_num")

        metrics = []
        upstream = version.value.decode()
        err = ctypes.c_double(0)
        for a in range(num.value):
            for b in (a + 1,):
                if b >= num.value:
                    continue
                rc = self._lib.QDMI_device_query_operation_error(
                    _TWO_QUBIT_OP, a, b, ctypes.byref(err))
                if rc == 0:
                    metrics.append({
                        "name": f"gate.2q.{_TWO_QUBIT_OP.decode()}.error",
                        "value": err.value, "qubits": [a, b],
                        "methodology": "qdmi-relayed", "upstream": upstream,
                    })
            rc = self._lib.QDMI_device_query_readout_error(a, ctypes.byref(err))
            if rc == 0:
                metrics.append({
                    "name": "readout.error", "value": err.value, "qubits": [a],
                    "methodology": "qdmi-relayed", "upstream": upstream,
                })
        return {
            "resource_id": name.value.decode(),
            "resource_type": "qdmi-device",
            "technology": "superconducting",  # site recipe: set per device
            "cloud": False,                   # QDMI devices are site-local
            "num_qubits": num.value,
            "metrics": metrics,
            "metadata": {"library": self._library_path, "version": upstream},
        }

    def start(self, qasm3: str, shots: int) -> str:
        handle = ctypes.c_void_p()
        rc = self._lib.QDMI_device_job_submit(qasm3.encode(), shots, ctypes.byref(handle))
        if rc != 0:
            raise RuntimeError(f"QDMI_device_job_submit rc={rc}")
        task_id = str(uuid.uuid4())
        with self._lock:
            self._jobs[task_id] = handle
        return task_id

    def status(self, task_id: str) -> tuple[str, str]:
        with self._lock:
            handle = self._jobs.get(task_id)
        if handle is None:
            return "FAILED", "unknown QDMI job"
        st = ctypes.c_int(0)
        rc = self._lib.QDMI_device_job_status(handle, ctypes.byref(st))
        if rc != 0:
            return "FAILED", f"QDMI_device_job_status rc={rc}"
        return _STATUS.get(st.value, "FAILED"), ""

    def result(self, task_id: str) -> dict:
        with self._lock:
            handle = self._jobs[task_id]
        keys = ((ctypes.c_char * 8) * 64)()
        values = (ctypes.c_long * 64)()
        entries = ctypes.c_size_t(0)
        rc = self._lib.QDMI_device_job_result_hist(
            handle, keys, values, 64, ctypes.byref(entries))
        if rc != 0:
            raise RuntimeError(f"QDMI_device_job_result_hist rc={rc}")
        return {
            keys[i].value.decode(): int(values[i]) for i in range(entries.value)
        }

    def stop(self, task_id: str) -> None:
        with self._lock:
            handle = self._jobs.get(task_id)
        if handle is not None:
            self._lib.QDMI_device_job_cancel(handle)

    def close(self) -> None:
        with self._lock:
            jobs, self._jobs = list(self._jobs.values()), {}
        for handle in jobs:
            self._lib.QDMI_device_job_free(handle)
        self._lib.QDMI_device_finalize()

    def _check(self, rc: int, what: str) -> None:
        if rc != 0:
            raise RuntimeError(f"QDMI {what} rc={rc}")
