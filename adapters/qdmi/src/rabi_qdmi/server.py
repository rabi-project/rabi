# SPDX-License-Identifier: Apache-2.0
"""rabi-adapter-qdmi: serve a QDMI device library as a Tangle target.

    rabi-adapter-qdmi --device /path/to/libdevice.so --listen :50054

CI certifies through the bundled mock device (mock/mock_device.c),
compiled by tests/hack scripts; a real site follows
docs/qdmi-site-recipe.md.
"""

from __future__ import annotations

import argparse
import logging
import signal
import threading
from concurrent import futures
from pathlib import Path

import grpc

from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from .device import QdmiDevice
from .service import QdmiAdapterService

log = logging.getLogger("rabi_qdmi")


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="[::]:50054")
    parser.add_argument("--device", required=True,
                        help="path to the QDMI device shared library")
    parser.add_argument("--target-id", default="",
                        help="target id (default: derived from the library name)")
    args = parser.parse_args()

    device = QdmiDevice(args.device)
    target_id = args.target_id or Path(args.device).stem.removeprefix("lib")
    log.info("serving QDMI device %s as target %s", args.device, target_id)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pb_grpc.add_AdapterServiceServicer_to_server(
        QdmiAdapterService({target_id: device}), server)
    server.add_insecure_port(args.listen)
    server.start()

    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    stop.wait()
    device.close()
    server.stop(grace=2)


if __name__ == "__main__":
    main()
