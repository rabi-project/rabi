#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Install the Rabi CLI (rabi). Usage:
#   curl -fsSL https://rabi-project.github.io/rabi/install.sh | sh
#   curl -fsSL https://rabi-project.github.io/rabi/install.sh | sh -s -- --version v0.4.1
#
# Downloads the matching prebuilt binary from GitHub Releases, verifies its
# SHA-256 against the release SHA256SUMS, and installs it to a directory on
# your PATH. No repo clone, no Go toolchain.
set -eu

REPO="rabi-project/rabi"
BIN="rabi"
VERSION="latest"

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --bin)     BIN="$2";     shift 2 ;;   # rabi | rabi | rabi-conformance
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

err() { echo "install: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- platform detection ------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux)  os=linux ;;
  darwin) os=darwin ;;
  *) err "unsupported OS '$os' (linux and darwin only)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) err "unsupported architecture '$arch'" ;;
esac
asset="${BIN}-${os}-${arch}"

# --- resolve version ---------------------------------------------------------
api="https://api.github.com/repos/${REPO}/releases"
if [ "$VERSION" = "latest" ]; then
  tag=$(curl -fsSL "${api}/latest" | grep -m1 '"tag_name"' | cut -d'"' -f4)
  [ -n "$tag" ] || err "could not resolve the latest release tag"
else
  tag="$VERSION"
fi
base="https://github.com/${REPO}/releases/download/${tag}"

# --- download + verify -------------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading ${asset} (${tag})..."
curl -fSL --progress-bar "${base}/${asset}" -o "${tmp}/${asset}" \
  || err "download failed for ${asset} @ ${tag} (is that platform published?)"

if curl -fsSL "${base}/SHA256SUMS" -o "${tmp}/SHA256SUMS" 2>/dev/null; then
  want=$(grep " ${asset}\$" "${tmp}/SHA256SUMS" | awk '{print $1}')
  if [ -n "$want" ]; then
    if have sha256sum; then got=$(sha256sum "${tmp}/${asset}" | awk '{print $1}')
    elif have shasum; then got=$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')
    else got=""; fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      err "checksum mismatch for ${asset} (expected ${want}, got ${got})"
    fi
    [ -n "$got" ] && echo "checksum ok"
  fi
else
  echo "warning: SHA256SUMS not found for ${tag}; skipping verification" >&2
fi
chmod +x "${tmp}/${asset}"

# --- install to a PATH dir ---------------------------------------------------
for dir in /usr/local/bin /opt/homebrew/bin "$HOME/.local/bin"; do
  case ":$PATH:" in *":$dir:"*) target="$dir"; break ;; esac
done
target="${target:-$HOME/.local/bin}"
mkdir -p "$target" 2>/dev/null || true

dest="${target}/${BIN}"
if [ -w "$target" ]; then
  mv "${tmp}/${asset}" "$dest"
elif have sudo; then
  echo "installing to ${dest} (needs sudo)"
  sudo mv "${tmp}/${asset}" "$dest"
else
  err "cannot write ${target}; re-run with a writable PATH dir or install sudo"
fi

echo "installed ${BIN} ${tag} -> ${dest}"
case ":$PATH:" in
  *":$target:"*) : ;;
  *) echo "note: ${target} is not on your PATH; add it or move ${BIN}" >&2 ;;
esac
"$dest" --help >/dev/null 2>&1 && echo "run '${BIN} --help' to get started."
