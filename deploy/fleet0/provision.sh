#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# fleet-0 provisioner: clone at the pinned tag, generate a bootstrap token,
# PULL prebuilt amd64 images from GHCR (never build on the box), install a
# systemd unit so the stack survives reboots. Idempotent.
set -euo pipefail
source /opt/rabi/provision.env 2>/dev/null || true
RABI_TAG="${RABI_TAG:-main}"
RABI_IMAGE_TAG="${RABI_IMAGE_TAG:-latest}"
RABI_PROBE_EVERY="${RABI_PROBE_EVERY:-15m}"

mkdir -p /opt/rabi && cd /opt/rabi
[ -d repo ] || git clone --branch "${RABI_TAG}" https://github.com/rabi-project/rabi.git repo
cd repo && git fetch --tags -q && git checkout -q "${RABI_TAG}"

[ -f /opt/rabi/bootstrap.token ] || openssl rand -hex 24 > /opt/rabi/bootstrap.token
chmod 600 /opt/rabi/bootstrap.token

COMPOSE="docker compose \
  -f deploy/compose/docker-compose.yml \
  -f deploy/fleet0/compose.images.yml \
  --profile observability"

# Render deploy/compose/.env (compose v2 reads it from the compose file's
# dir). A script file avoids systemd specifier expansion (%s = user shell).
cat > /opt/rabi/render-env.sh <<RENDER
#!/usr/bin/env bash
{
  echo "RABI_BOOTSTRAP_TOKEN=\$(cat /opt/rabi/bootstrap.token)"
  echo "RABI_IMAGE_TAG=${RABI_IMAGE_TAG}"
  echo "RABI_PROBE_EVERY=${RABI_PROBE_EVERY}"
} > /opt/rabi/repo/deploy/compose/.env
# Authenticate to GHCR if a pull token is present (private packages).
if [ -s /opt/rabi/ghcr.token ]; then
  docker login ghcr.io -u inch900 --password-stdin < /opt/rabi/ghcr.token >/dev/null 2>&1 || true
fi
RENDER
chmod +x /opt/rabi/render-env.sh

cat > /etc/systemd/system/rabi-fleet0.service <<UNIT
[Unit]
Description=Rabi fleet-0 (compose, pulls prebuilt images)
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=true
WorkingDirectory=/opt/rabi/repo
Environment=RABI_IMAGE_TAG=${RABI_IMAGE_TAG}
Environment=RABI_PROBE_EVERY=${RABI_PROBE_EVERY}
# compose v2 reads .env from the compose file's directory, not the cwd.
ExecStartPre=/opt/rabi/render-env.sh
ExecStartPre=/usr/bin/${COMPOSE} pull
ExecStart=/usr/bin/${COMPOSE} up -d --no-build --wait
ExecStop=/usr/bin/${COMPOSE} down

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now rabi-fleet0.service
echo "fleet-0 up: console :8080/console/, grafana :3000; token in /opt/rabi/bootstrap.token"
