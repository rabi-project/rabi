# Installing Rabi

Rabi has two parts: the **`rabi` command-line client** (what you install on
your machine to talk to a fleet) and the **`rabi` control plane** (what an
operator deploys). This page covers installing the client; to deploy a fleet,
see the [site install guide](site-install-guide.md).

## Install `rabi`

### One-line installer (macOS, Linux)

```sh
curl -fsSL https://rabi-project.github.io/rabi/install.sh | sh
```

Detects your OS/architecture, downloads the matching binary from GitHub
Releases, verifies its SHA-256, and installs it to a directory on your PATH.
No repo clone, no Go toolchain. Pin a version or pick another binary:

```sh
curl -fsSL https://rabi-project.github.io/rabi/install.sh | sh -s -- --version v0.4.1
curl -fsSL https://rabi-project.github.io/rabi/install.sh | sh -s -- --bin rabi-conformance
```

### Homebrew (macOS, Linux)

```sh
brew install rabi-project/tap/rabi
```

Uses the [Rabi Homebrew tap](https://github.com/rabi-project/homebrew-tap).
`brew upgrade rabi` keeps it current.

### With Go

```sh
go install github.com/rabi-project/rabi/cmd/rabi@latest
```

### Manual download

Grab the binary for your platform from the
[latest release](https://github.com/rabi-project/rabi/releases/latest)
(`rabi-<os>-<arch>`), verify it against `SHA256SUMS`, `chmod +x`, and move it
onto your PATH.

## Verify

```sh
rabi --help
rabi --version        # prints the release version
```

## Point it at a fleet

```sh
export RABI_SERVER=your-fleet-host:9090
export RABI_TOKEN=<your API token>      # or: rabi login
rabi targets
```

See the [rabi reference](rabi-reference.md) for every command and the
[concepts guide](concepts.md) for the model.

## Deploy a control plane

The server side is not a single binary you `brew install` — it needs Postgres
and adapters. Deploy it with Helm or Docker Compose per the
[site install guide](site-install-guide.md), or pull the published image
directly:

```sh
docker pull ghcr.io/rabi-project/rabi:latest
```
