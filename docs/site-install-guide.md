# Site install guide (pilot)

Target: a working fleet in ≤ 60 minutes on a clean Linux VM (4+ cores,
8 GB RAM, Docker with Compose) or a Kubernetes cluster. Every command is
copy-pasteable; nothing needs internet at runtime if you use the offline
bundle.

## Path A — compose on a VM (fleet-0's shape, ~20 min)

```sh
git clone https://github.com/rabi-project/rabi.git && cd rabi   # or unpack the release tarball
export RABI_BOOTSTRAP_TOKEN="$(openssl rand -hex 24)"           # first-admin credential
export RABI_PROBE_EVERY=5m
docker compose -f deploy/compose/docker-compose.yml \
  --profile observability up -d --build --wait
```

Verify (5 min):

```sh
export RABI_TOKEN="$RABI_BOOTSTRAP_TOKEN"
go run ./cmd/rabi whoami          # bootstrap admin
go run ./cmd/rabi targets         # 3 replay QPUs online
```

- Console: http://VM:8080/console/ (paste the token)
- Grafana: http://VM:3000 (Rabi → fleet health; probes appear within
  RABI_PROBE_EVERY)

## Path B — Helm (~30 min)

```sh
helm install rabi deploy/helm/rabi \
  --set auth.bootstrapToken="$(openssl rand -hex 24)"
kubectl port-forward svc/rabi 9090:9090 8080:8080
```

Air-gapped: build `deploy/airgap/build-bundle.sh` on a connected machine,
move the tarball, run its `install.sh` (verified no-egress in CI).

## Production auth (10 min)

1. Point at your IdP: set `RABI_OIDC_ISSUER`, `RABI_OIDC_CLIENT_ID`,
   map groups via `RABI_OIDC_GROUP_ROLES` (e.g. `qc-admins=admin`).
2. `rabi login` → `rabi token create ci-bot --project <org/team> --role member`.
3. Unset `RABI_BOOTSTRAP_TOKEN` and restart. Done — no static secrets.

## Wire your hardware (15 min)

Run the adapter for your access path (each is certified — reports ship
with the release):

```sh
rabi-adapter-ibm                 # IBM_TOKEN in env
rabi-adapter-qrmi --resource my-backend=IBMQiskitRuntimeService
rabi-adapter-qdmi --device /opt/qdmi/libdevice.so   # see docs/qdmi-site-recipe.md
rabi-adapter-iqm --server https://cocos.resonance.meetiqm.com/<qc>
```

Then add it to the fleet: `RABI_ADAPTERS="sim=...,mine=host:5005x"` and
restart `rabi`. `rabi targets` shows it; probes cover it automatically on
the next tick.

## Acceptance for this guide

Work through docs/security-checklist.md, run one Bell job end to end
(`rabi submit -f examples/bell.yaml`, watch the placement audit in the
console), and confirm `reconciliation clean` appears in logs within a
week (or force with RABI_RECONCILE_EVERY=1h).
