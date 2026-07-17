# SPDX-License-Identifier: Apache-2.0
# Build context is the repo root (needs adapters/aer for the protocol stubs).
FROM python:3.13-slim
WORKDIR /app/adapters/ibm
COPY adapters/aer /app/adapters/aer
COPY adapters/ibm/pyproject.toml ./
COPY adapters/ibm/src ./src
RUN pip install --no-cache-dir /app/adapters/aer && pip install --no-cache-dir .
EXPOSE 50052
ENTRYPOINT ["tangle-adapter-ibm"]
