# Relayward

Relayward is a single-administrator control plane for managing lightweight proxy nodes through a native Agent and installable runtime plugins.

The first release is intentionally limited to the control-plane kernel and plugin framework. Proxy protocols, Xray, sing-box, risk analysis, and notification channels are separate plugins or later components.

## Repository Layout

```text
cmd/relayward/       service entry point
internal/server/     HTTP server and system endpoints
web/                 React administration interface
```

## Local Checks

```bash
go test ./...
go vet ./...
go build ./cmd/relayward

cd web
npm ci
npm run typecheck
npm test
npm run build
```

Run the control plane and Vite development server locally:

```bash
go run ./cmd/relayward serve -listen 127.0.0.1:8080 -data ./data -insecure-cookie
curl http://127.0.0.1:8080/healthz

cd web
npm run dev
```

The Vite server proxies API requests to `127.0.0.1:8080`. Production session cookies require HTTPS; `-insecure-cookie` exists only for loopback development.

The persistent data directory contains the primary `relayward.db`, the independent hot-event `events.db`, and the instance encryption key. Back up and restore the directory as one unit.

If the persisted instance key is lost and no recovery code is available, reset TOTP locally and revoke all administrator sessions:

```bash
go run ./cmd/relayward admin reset-totp -data ./data
```

The approved product scope and implementation sequence are maintained in the Relayward workspace plan. This repository does not provide compatibility with 3x-ui or the previous xui-stack databases.

## Docker Compose

Relayward publishes one Linux AMD64 image to `ghcr.io/relayward/relayward`. Copy `compose.yaml` and `.env.example` to the deployment directory, rename `.env.example` to `.env`, and set `RELAYWARD_VERSION` to the release being deployed. Start the service with:

```bash
docker compose pull
docker compose up -d
docker compose exec relayward relayward healthcheck
```

The example binds `127.0.0.1:8080` by default. Terminate HTTPS at a reverse proxy and forward all paths, including WebSocket upgrades, to that address. Do not expose the plain HTTP port directly to the Internet. Production session cookies are always marked secure.

All databases, the instance encryption key, plugin artifacts, plugin state, and event archives live in the `relayward-data` volume. Back up and restore that volume as one unit. Stop the container or use a storage snapshot that preserves a point-in-time consistent volume.

To upgrade, change `RELAYWARD_VERSION`, then run `docker compose pull && docker compose up -d`. To roll back the center, restore the previous version value and recreate the container. Database migrations are forward-only, so retain a matching volume snapshot before upgrading across released versions.
