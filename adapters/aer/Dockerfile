# SPDX-License-Identifier: Apache-2.0
# Build context is the repo root (compose sets dockerfile: adapters/aer/...)
# so the image can carry bench/data/snapshots for the replay fleet.
FROM python:3.13-slim
WORKDIR /app/adapters/aer
COPY adapters/aer/pyproject.toml ./
COPY adapters/aer/src ./src
RUN pip install --no-cache-dir .
COPY adapters/aer/config ./config
COPY bench/data /app/bench/data
EXPOSE 50051
ENTRYPOINT ["rabi-adapter-aer"]
CMD ["--config", "config/replay.yaml", "--listen", "[::]:50051"]
