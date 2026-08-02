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

If the persisted instance key is lost and no recovery code is available, reset TOTP locally and revoke all administrator sessions:

```bash
go run ./cmd/relayward admin reset-totp -data ./data
```

The approved product scope and implementation sequence are maintained in the Relayward workspace plan. This repository does not provide compatibility with 3x-ui or the previous xui-stack databases.
