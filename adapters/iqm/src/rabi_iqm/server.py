# SPDX-License-Identifier: Apache-2.0
"""rabi-adapter-iqm: serve IQM Resonance as a Tangle target.

Live (needs the `iqm` extra + IQM_TOKEN in the environment the SDK reads):

    rabi-adapter-iqm --server https://cocos.resonance.meetiqm.com/<qc> --listen :50055

Cassette mode (tokenless, deterministic — what CI certifies):

    rabi-adapter-iqm --cassette --listen :50055
"""

from __future__ import annotations

import argparse
import logging
import signal
import threading
from concurrent import futures

import grpc

from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from .backends import CassetteIqm, LiveIqm
from .service import IqmAdapterService

log = logging.getLogger("rabi_iqm")


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="[::]:50055")
    parser.add_argument("--server", default="", help="IQM Resonance server URL (live mode)")
    parser.add_argument("--quantum-computer", default="", help="named QC on the server")
    parser.add_argument(
        "--cassette", action="store_true",
        help="serve the deterministic cassette resource (tokenless; CI certification)")
    args = parser.parse_args()

    resources: dict[str, object] = {}
    if args.cassette:
        resources["cassette-iqm"] = CassetteIqm()
    if args.server:
        resources["iqm"] = LiveIqm(args.server, args.quantum_computer)
    if not resources:
        raise SystemExit("nothing to serve: pass --server URL and/or --cassette")

    log.info("serving IQM resources: %s", list(resources))
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pb_grpc.add_AdapterServiceServicer_to_server(IqmAdapterService(resources), server)
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
