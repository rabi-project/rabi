# SPDX-License-Identifier: Apache-2.0
"""tangle-adapter-aer: serve tangle.adapter.v1alpha1 over Qiskit Aer."""

from __future__ import annotations

import argparse
import logging
import os
import signal
import threading
from concurrent import futures

import grpc

from tangle.adapter.v1alpha1 import adapter_pb2_grpc as pb_grpc

from .fleet import parse_rfc3339
from .replay import ReplayClock
from .service import AdapterService
from .targets import load_config

log = logging.getLogger("tangle_aer")


def serve(config_path: str, listen: str) -> grpc.Server:
    targets = load_config(config_path)

    # Fleet-wide replay clock: 1 wall second = TANGLE_SIM_ACCEL sim seconds,
    # anchored at the earliest calibration baseline (mvp-build-plan.md §4).
    accel = float(os.environ.get("TANGLE_SIM_ACCEL", "1"))
    epoch = min(parse_rfc3339(t.snapshot.measured_at) for t in targets)
    clock = ReplayClock(epoch=epoch, accel=accel)
    if any(t.drift for t in targets):
        log.info("calibration replay on: epoch=%s accel=%gx", epoch.isoformat(), accel)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=32))
    pb_grpc.add_AdapterServiceServicer_to_server(AdapterService(targets, clock), server)
    bound = server.add_insecure_port(listen)
    server.start()
    log.info("serving %d target(s) on port %d: %s",
             len(targets), bound, [t.target_id for t in targets])
    return server


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, help="targets YAML")
    parser.add_argument("--listen", default="[::]:50051")
    args = parser.parse_args()

    server = serve(args.config, args.listen)
    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    stop.wait()
    server.stop(grace=5).wait()


if __name__ == "__main__":
    main()
