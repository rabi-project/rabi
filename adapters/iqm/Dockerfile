# SPDX-License-Identifier: Apache-2.0
# Build context is the repo root (needs aer stubs + the qrmi chassis).
FROM python:3.13-slim
WORKDIR /app/adapters/iqm
COPY adapters/aer /app/adapters/aer
COPY adapters/qrmi /app/adapters/qrmi
COPY adapters/iqm/pyproject.toml ./
COPY adapters/iqm/src ./src
RUN pip install --no-cache-dir /app/adapters/aer /app/adapters/qrmi && \
    pip install --no-cache-dir .
EXPOSE 50055
ENTRYPOINT ["rabi-adapter-iqm"]
