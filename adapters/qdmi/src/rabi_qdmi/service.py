# SPDX-License-Identifier: Apache-2.0
"""QDMI adapter service: the shared chassis over QdmiDevice backends."""

from __future__ import annotations

from rabi_qrmi.service import QrmiAdapterService


class QdmiAdapterService(QrmiAdapterService):
    VENDOR = "qdmi"
    SNAPSHOT_PREFIX = "qdmi"
    MAX_SHOTS = 100_000

    def _extensions(self, d: dict) -> dict[str, str]:
        return {
            "technology": d["technology"],
            "cloud": "true" if d["cloud"] else "false",
            "qdmi-library": str(d["metadata"].get("library", "")),
            "qdmi-device-version": str(d["metadata"].get("version", "")),
        }
