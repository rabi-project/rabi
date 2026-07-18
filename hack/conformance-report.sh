#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# M7 acceptance: from a clean checkout, one command certifies the in-tree
# adapters and produces the signed report artifacts:
#
#   ./hack/conformance-report.sh [out-dir]     (default bin/conformance)
#
# Aer runs in replay-target mode; IBM runs in --fake mode (tokenless,
# deterministic — the report carries an explicit note). Live-IBM
# certification runs nightly with credentials.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="${1:-bin/conformance}"
mkdir -p "$OUT"

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
go build -ldflags "-X main.version=$VERSION" -o bin/rabi-conformance ./cmd/rabi-conformance

run_one() { # name, spawn-dir, spawn-cmd..., extra CLI args
  local name="$1" dir="$2"; shift 2
  local port addr pid
  port="$(free_port)"
  addr="127.0.0.1:$port"

  echo "--- $name adapter on $addr"
  # Fully detach the spawned tree's stdio: an orphaned child must never
  # hold this script's output pipe open.
  (cd "$dir" && exec "$@" --listen "$addr") >/dev/null 2>&1 &
  pid=$!

  for _ in $(seq 1 60); do
    if python3 -c "import socket; socket.create_connection(('127.0.0.1', $port), 1).close()" 2>/dev/null; then
      break
    fi
    sleep 1
  done

  local rc=0
  case "$name" in
  ibm)
    bin/rabi-conformance run --target "$addr" --out "$OUT/$name" \
      --note "fake-backend mode (qiskit-ibm-runtime FakeManilaV2); live certification runs nightly" || rc=$?
    ;;
  qrmi)
    bin/rabi-conformance run --target "$addr" --out "$OUT/$name" \
      --note "cassette mode (deterministic QRMI-shaped resource); live certification runs nightly with credentials" || rc=$?
    ;;
  qdmi)
    bin/rabi-conformance run --target "$addr" --out "$OUT/$name" \
      --note "mock QDMI device (compiled C library; the ctypes ABI path is real) — real-site recipe in docs/qdmi-site-recipe.md" || rc=$?
    ;;
  *)
    bin/rabi-conformance run --target "$addr" --out "$OUT/$name" || rc=$?
    ;;
  esac
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  return "$rc"
}

run_one aer adapters/aer uv run rabi-adapter-aer --config config/single.yaml
run_one ibm adapters/ibm uv run rabi-adapter-ibm --fake
run_one qrmi adapters/qrmi uv run rabi-adapter-qrmi --cassette

echo "--- qdmi mock device (C ABI)"
case "$(uname -s)" in Darwin) MOCK_EXT=dylib ;; *) MOCK_EXT=so ;; esac
cc -shared -fPIC -o "bin/libmockqdmi.$MOCK_EXT" adapters/qdmi/mock/mock_device.c
run_one qdmi adapters/qdmi uv run rabi-adapter-qdmi --device "$PWD/bin/libmockqdmi.$MOCK_EXT"

echo "CONFORMANCE-REPORTS OK ($OUT/aer, $OUT/ibm, $OUT/qrmi, $OUT/qdmi)"
