# SPDX-License-Identifier: Apache-2.0
"""IQM adapter service: the shared chassis with IQM identity."""

from __future__ import annotations

from rabi_qrmi.service import QrmiAdapterService


class IqmAdapterService(QrmiAdapterService):
    VENDOR = "iqm"
    SNAPSHOT_PREFIX = "iqm"
    MAX_SHOTS = 100_000

    def _extensions(self, d: dict) -> dict[str, str]:
        return {
            "technology": d["technology"],
            "cloud": "true" if d["cloud"] else "false",
            "iqm-server": str(d["metadata"].get("server", d["metadata"].get("mode", ""))),
        }
