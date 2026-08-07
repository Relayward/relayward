# Relayward

[简体中文](README.md) | [English](README.en.md)

[![CI](https://github.com/Relayward/relayward/actions/workflows/ci.yml/badge.svg)](https://github.com/Relayward/relayward/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Relayward/relayward)](https://github.com/Relayward/relayward/releases)
[![License](https://img.shields.io/github/license/Relayward/relayward)](LICENSE)

Relayward is a single-administrator node control plane for administrator authentication, node enrollment, users, authorizations, quotas, subscription entry points, audit history, and plugin lifecycle. Nodes connect through a compatible Agent, while independent plugins provide runtime-specific capabilities.

Relayward is under active development. Current releases target self-hosted Linux AMD64 deployments.

## Features

- Single-administrator password authentication, TOTP, recovery codes, session management, and local administrator recovery
- Node records, one-time Agent enrollment, outbound-only control connections, and online-state management
- Reliable command and event delivery, policy reconciliation, Agent updates, and rollback coordination
- Per-user, per-node policy configuration and delivery for quotas, expiry, reset periods, and soft source-IP limits
- Plugin service catalogs, authorization service bindings, and a unified subscription entry point
- Plugin release inspection, permission approval, artifact verification, process supervision, upgrade rollback, and sandboxed administration pages
- Simplified Chinese and English administration interface with light and dark themes
- SQLite state, encrypted secrets, audit history, hot events, and compressed event archives

## Architecture

```text
Browser
   |
 HTTPS
   |
Relayward center (Docker)
   |
   | outbound WebSocket control connection
   |
Relayward Agent (native service)
   |
   | supervised local plugin process
   |
Runtime or feature plugin (independent project)
```

The center manages only generic node, policy, service, and plugin state. It does not interpret runtime-specific configuration or run and package a proxy core. Agents do not expose a management port; they initiate the control connection to the center.

## Requirements

| Component | Supported environment |
| --- | --- |
| Center | Linux AMD64, Docker Engine, Docker Compose v2 |
| Public access | A domain name and an HTTPS reverse proxy with WebSocket support |
| Nodes | Linux AMD64 running Debian/systemd or Alpine/OpenRC |
| Network | Nodes require outbound HTTPS access to the center, GitHub Releases, and runtime release hosts |

The example deployment binds Relayward to `127.0.0.1:8080`. Do not expose this plain HTTP listener directly to the Internet.

## Quick Start

### 1. Deploy The Center

Clone the repository on the center host and create the deployment environment:

```bash
git clone --depth 1 https://github.com/Relayward/relayward.git
cd relayward
cp .env.example .env
```

Review `.env` before starting. `RELAYWARD_VERSION` must be an existing image tag from [Relayward Releases](https://github.com/Relayward/relayward/releases); production deployments should use an explicit version instead of `latest`.

```bash
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose exec relayward relayward healthcheck
docker compose logs --tail=100 relayward
```

The health check must report success before continuing. All persistent state is stored in the `relayward-data` Docker volume.

### 2. Configure HTTPS

Terminate TLS at a reverse proxy and forward every path, including WebSocket upgrades, to `127.0.0.1:8080`. A minimal Caddy site is:

```caddyfile
relayward.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

For Nginx or a hosting panel, preserve the original `Host` header and enable HTTP/1.1 WebSocket forwarding. Relayward production session cookies require HTTPS.

### 3. Initialize The Administrator

Open `https://relayward.example.com` in a browser and create the single administrator. Passwords must contain at least 12 characters.

After signing in:

1. Open **Settings** and set **Public URL** to the HTTPS origin, for example `https://relayward.example.com`. Do not include a path, query, credentials, or trailing application route.
2. Set the deployment timezone.
3. Open **Security**, enable TOTP, and store the generated recovery codes outside the Relayward data volume.

### 4. Register A Node

In **Nodes**, select **Add node**, enter a name and the public hostname or IP used by subscriptions, then select **Register Agent**. Relayward generates a short-lived, single-use root installation command.

Run that generated command on the node. It downloads the latest official [Relayward Agent](https://github.com/Relayward/relayward-agent) release, verifies its checksums, installs the correct systemd or OpenRC service, enrolls the node, and starts the Agent.

The registration dialog should change to **Agent registered and online**. The equivalent service checks are:

```bash
# Debian/systemd
systemctl status relayward-agent.service
journalctl -u relayward-agent.service -n 100 --no-pager

# Alpine/OpenRC
rc-service relayward-agent status
tail -n 100 /var/log/messages
```

The Agent needs no inbound firewall rule. It connects to the center over HTTPS.

### 5. Select And Install A Plugin

Relayward itself does not provide proxy protocols or a proxy core. Select a compatible runtime plugin and use that plugin repository's README as the authority for installation, node configuration, network exposure, and verification.

To install a plugin, open **Plugins**, select **Install plugin**, and enter its GitHub repository, an explicit release version, and an optional token when the repository is private. Select **Check release**, review the manifest and artifacts, approve every requested permission, and install it.

Current official runtime plugins are listed under [Related Projects](#related-projects). Protocols, configuration fields, service ports, subscription formats, and acceptance criteria belong to each plugin and are documented in its repository.

### 6. Create Access

1. Open **Users** and create a user.
2. Open **Authorizations** and add an authorization for that user and node.
3. Configure quota, reset period, expiry, timezone, and optional soft IP limit.
4. After an installed plugin publishes services, use **Manage services** to bind the authorization to one or more of them.
5. Copy the subscription link generated for the authorization.

Relayward owns authorization state, traffic metadata, and the unified subscription entry point. Installed plugins provide the concrete connection content and output formats.

## Verification

After deployment, verify all of the following:

- `docker compose exec relayward relayward healthcheck` succeeds.
- The administration page is available only through HTTPS.
- The node reports online and shows its Agent version.
- Installed center plugins report an active and healthy state.
- The authorization reports active after policy delivery.

Continue with the selected plugin's README to verify runtime services, node-plugin instances, subscription content, and client connectivity.

## Operations

The `relayward-data` volume contains the primary database, hot-event database, instance encryption key, plugin artifacts, plugin state, and event archives. Back up and restore it as one unit.

See [Operations](docs/operations.md) for supported backup, upgrade, rollback, administrator recovery, instance-key recovery, and node credential recovery procedures. Database migrations are forward-only; take a complete volume snapshot before upgrading released versions.

## Development

Backend checks:

```bash
go test ./...
go vet ./...
go build ./cmd/relayward
```

Frontend checks:

```bash
cd web
npm ci
npm run typecheck
npm test
npm run build
```

Run the backend and Vite development server locally:

```bash
go run ./cmd/relayward serve -listen 127.0.0.1:8080 -data ./data -insecure-cookie

cd web
npm run dev
```

`-insecure-cookie` is for loopback development only. The Vite server proxies API requests to `127.0.0.1:8080`.

## Related Projects

- [Relayward Agent](https://github.com/Relayward/relayward-agent)
- [Relayward SDK](https://github.com/Relayward/relayward-sdk)
- [Relayward Xray Plugin](https://github.com/Relayward/relayward-plugin-xray)

## Security

Treat plugins as trusted native executables and install them only from repositories you trust. Do not publish registration tokens, subscription links, recovery codes, GitHub tokens, private configuration, or Relayward data volumes in issue reports.

Report security issues privately through the repository's [GitHub Security Advisories](https://github.com/Relayward/relayward/security/advisories/new).

## License

Relayward is licensed under the [Apache License 2.0](LICENSE).
