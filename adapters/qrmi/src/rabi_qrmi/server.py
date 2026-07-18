# SPDX-License-Identifier: Apache-2.0
"""rabi-adapter-qrmi: serve QRMI-managed resources as Tangle targets.

Live mode (needs the `qrmi` extra + provider credentials in QRMI's .env):

    rabi-adapter-qrmi --resource mybackend=IBMQiskitRuntimeService --listen :50053

Cassette mode (tokenless, deterministic — what CI certifies):

    rabi-adapter-qrmi --cassette --listen :50053
"""

from __future__ import annotations

import argparse
import logging
import signal
import threading
from concurrent import futures

import grpc

from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from .backends import CassetteQrmi, LiveQrmi
from .service import QrmiAdapterService

log = logging.getLogger("rabi_qrmi")


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="[::]:50053")
    parser.add_argument(
        "--resource",
        action="append",
        default=[],
        metavar="ID=TYPE",
        help="QRMI resource to serve (repeatable), e.g. mybackend=IBMQiskitRuntimeService",
    )
    parser.add_argument(
        "--cassette",
        action="store_true",
        help="serve the deterministic cassette resource (tokenless; for "
        "conformance certification in CI — reports carry a cassette note)",
    )
    args = parser.parse_args()

    resources: dict[str, object] = {}
    if args.cassette:
        resources["cassette-qrmi"] = CassetteQrmi("cassette-qrmi")
    for spec in args.resource:
        rid, _, rtype = spec.partition("=")
        if not rid or not rtype:
            raise SystemExit(f"--resource {spec!r}: want ID=TYPE")
        resources[rid] = LiveQrmi(rid, rtype)
    if not resources:
        raise SystemExit("nothing to serve: pass --resource ID=TYPE and/or --cassette")

    log.info("serving QRMI resources: %s", list(resources))
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pb_grpc.add_AdapterServiceServicer_to_server(QrmiAdapterService(resources), server)
    server.add_insecure_port(args.listen)
    server.start()

    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    stop.wait()
    for r in resources.values():
        r.close()
    server.stop(grace=2)


if __name__ == "__main__":
    main()
