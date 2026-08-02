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

Run the current service skeleton locally:

```bash
go run ./cmd/relayward -listen 127.0.0.1:8080
curl http://127.0.0.1:8080/healthz
```

The approved product scope and implementation sequence are maintained in the Relayward workspace plan. This repository does not provide compatibility with 3x-ui or the previous xui-stack databases.
