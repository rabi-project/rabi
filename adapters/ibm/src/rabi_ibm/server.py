# SPDX-License-Identifier: Apache-2.0
"""rabi-adapter-ibm: serve tangle.adapter.v1alpha1 over IBM Quantum.

Requires IBM_TOKEN (feature flag — the compose profile `ibm` is off by
default). Backends come from IBM_BACKENDS (comma-separated names, default:
the least-busy operational backend).
"""

from __future__ import annotations

import argparse
import logging
import os
import signal
import threading
from concurrent import futures

import grpc
from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from .service import IBMAdapterService

log = logging.getLogger("rabi_ibm")


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--listen", default="[::]:50052")
    args = parser.parse_args()

    token = os.environ.get("IBM_TOKEN")
    if not token:
        raise SystemExit("IBM_TOKEN is required (this adapter is feature-flagged off without it)")

    from qiskit_ibm_runtime import QiskitRuntimeService

    service = QiskitRuntimeService(channel="ibm_quantum_platform", token=token)
    names = [n for n in os.environ.get("IBM_BACKENDS", "").split(",") if n]
    if names:
        backends = {n: service.backend(n) for n in names}
    else:
        least_busy = service.least_busy(operational=True, simulator=False)
        backends = {least_busy.name: least_busy}
    log.info("serving IBM backends: %s (open-plan queue times may be hours)",
             list(backends))

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=16))
    pb_grpc.add_AdapterServiceServicer_to_server(IBMAdapterService(backends), server)
    server.add_insecure_port(args.listen)
    server.start()

    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    stop.wait()
    server.stop(grace=5).wait()


if __name__ == "__main__":
    main()
