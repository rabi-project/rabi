# SPDX-License-Identifier: Apache-2.0
FROM python:3.13-slim
WORKDIR /app
COPY pyproject.toml README.md* ./
COPY src ./src
RUN pip install --no-cache-dir .
COPY config ./config
EXPOSE 50051
ENTRYPOINT ["tangle-adapter-aer"]
CMD ["--config", "config/single.yaml", "--listen", "[::]:50051"]
