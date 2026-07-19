#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# fleet-0 provisioner: clone at the pinned tag, generate the bootstrap
# token, bring up compose with observability, install a systemd unit so the
# stack survives reboots. Idempotent.
set -euo pipefail
source /opt/rabi/provision.env 2>/dev/null || RABI_TAG=main

mkdir -p /opt/rabi && cd /opt/rabi
[ -d repo ] || git clone --branch "${RABI_TAG}" https://github.com/rabi-project/rabi.git repo
cd repo

[ -f /opt/rabi/bootstrap.token ] || openssl rand -hex 24 > /opt/rabi/bootstrap.token
chmod 600 /opt/rabi/bootstrap.token

cat > /etc/systemd/system/rabi-fleet0.service <<UNIT
[Unit]
Description=Rabi fleet-0 (compose)
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=true
WorkingDirectory=/opt/rabi/repo
Environment=RABI_PROBE_EVERY=${RABI_PROBE_EVERY:-15m}
ExecStartPre=/bin/bash -c 'printf "RABI_BOOTSTRAP_TOKEN=%s\nRABI_PROBE_EVERY=%s\n" "$(cat /opt/rabi/bootstrap.token)" "${RABI_PROBE_EVERY:-15m}" > /opt/rabi/repo/deploy/compose/.env'
ExecStart=/usr/bin/docker compose -f deploy/compose/docker-compose.yml --profile observability up -d --build --wait
ExecStop=/usr/bin/docker compose -f deploy/compose/docker-compose.yml --profile observability down

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now rabi-fleet0.service
echo "fleet-0 up: console :8080/console/, grafana :3000; token in /opt/rabi/bootstrap.token"
