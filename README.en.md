# Relayward

[简体中文](README.md) | [English](README.en.md)

[![CI](https://github.com/Relayward/relayward/actions/workflows/ci.yml/badge.svg)](https://github.com/Relayward/relayward/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Relayward/relayward)](https://github.com/Relayward/relayward/releases)
[![License](https://img.shields.io/github/license/Relayward/relayward)](LICENSE)

Relayward is a single-administrator node control plane for administrator authentication, node enrollment, users, authorizations, quotas, subscription entry points, audit history, and plugin lifecycle. Nodes connect through a compatible Agent, while independent plugins provide runtime-specific capabilities.

Relayward is under active development. Current releases target self-hosted Linux AMD64 deployments. Every `v0.x` release is a public development artifact and carries no persistent-data compatibility guarantee. Replacing a `v0.x` version requires an empty `.data` directory and fresh initialization; do not run a new version against a previous version's data directory. The first compatibility release will be identified explicitly in its release notes.

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
| Center | Linux AMD64, Docker Engine, Docker Compose v2, curl |
| Center access | A reachable HTTP address, or a domain name and an HTTPS reverse proxy with WebSocket support |
| Nodes | Linux AMD64 running Debian/systemd or Alpine/OpenRC |
| Network | Nodes require HTTP(S) access to the center and HTTPS access to GitHub Releases and runtime release hosts |

The example deployment binds Relayward to `127.0.0.1:8080` by default. For direct HTTP access, set `RELAYWARD_BIND_ADDRESS` in `.env` to a host address reachable by clients. HTTP does not encrypt login credentials, subscription tokens, or Agent data in transit.

## Quick Start

### 1. Deploy The Center

Enter an empty deployment directory of your choice and download the repository's Compose and environment examples directly:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Relayward/relayward/main/compose.yaml \
  -o compose.yaml
curl -fsSL \
  https://raw.githubusercontent.com/Relayward/relayward/main/.env.example \
  -o .env
chmod 600 .env
```

Review `.env` before starting and keep its permissions at `0600` because optional settings may contain secrets. `RELAYWARD_VERSION` must be a published image tag; production deployments should use an explicit version from [Relayward Releases](https://github.com/Relayward/relayward/releases) instead of `latest`. Use `dev` to follow the latest development code on `main`; it is updated only after non-documentation changes pass CI and carries no stability or data-compatibility guarantee. On first start, the container creates `.data` and prepares its permissions automatically while the Relayward process continues to run as a non-root user.

```bash
docker compose config --quiet
docker compose up -d --wait
docker compose exec relayward relayward healthcheck
docker compose logs --tail=100 relayward
```

The health check must report success before continuing. All persistent state is stored in `.data` under the current deployment directory.

### 2. Choose The Access Method

Relayward fully supports direct HTTP access; HTTPS is not required to start or use the system. With HTTP, administrator credentials, session data, registration credentials, commands, and telemetry travel without encryption, and the UI displays a persistent security warning. Use HTTP only on networks where you accept that risk.

Direct HTTP access also requires allowing the TCP port configured by `RELAYWARD_PORT` through both the cloud firewall and the host firewall. If the host uses UFW and keeps the default port, run `ufw allow 8080/tcp`; use the matching firewall tool and actual port in other environments.

An HTTPS reverse proxy is strongly recommended. Terminate TLS at the proxy and forward every path, including WebSocket upgrades, to `127.0.0.1:8080`. A minimal Caddy site is:

```caddyfile
relayward.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

`compose.yaml` also includes a commented Cloudflare Tunnel service. To use it, create a Tunnel and public hostname in Cloudflare, set its origin service to `http://relayward:8080`, add the Tunnel token to `.env` as `CLOUDFLARE_TUNNEL_TOKEN`, and uncomment the complete `cloudflared` service. The token is a secret and must not be committed.

For Nginx or a hosting panel, preserve the original `Host` header, pass `X-Forwarded-Proto`, and enable HTTP/1.1 WebSocket forwarding. Relayward selects cookie attributes from the request protocol: HTTPS requests receive `Secure` cookies, while HTTP requests receive regular cookies.

### 3. Initialize The Administrator

Open the selected HTTP or HTTPS address in a browser and create the single administrator.

After signing in:

1. Open **Settings** and review the automatically populated **Public URL**. It starts with the current browser origin and may be changed to another HTTP or HTTPS origin. Do not include a path, query, credentials, or trailing application route.
2. Set the deployment timezone.
3. Optional: open **Security** and enable TOTP. It is strongly recommended for deployments on public or untrusted networks. If enabled, store the generated recovery codes outside the Relayward `.data` directory.

### 4. Register A Node

In **Nodes**, select **Add node**, enter a name, then select **Register Agent**. Relayward generates a short-lived, single-use root installation command.

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

The Agent needs no inbound firewall rule. It connects outbound to the HTTP or HTTPS address configured as the center Public URL.

### 5. Select And Install A Plugin

Relayward itself does not provide proxy protocols or a proxy core. Select a compatible runtime plugin and use that plugin repository's README as the authority for installation, node configuration, network exposure, and verification.

To install a plugin, open **Plugins**, select **Install plugin**, and enter its GitHub repository and an optional token when the repository is private. Wait for the published release list to load, select the version to install, and then select **Check release**. Review the manifest and artifacts, approve every requested permission, and install it.

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
- The administration page is reachable at the selected HTTP or HTTPS address; HTTP access displays the unencrypted-transport warning.
- The node reports online and shows its Agent version.
- Installed center plugins report an active and healthy state.
- The authorization reports active after policy delivery.

Continue with the selected plugin's README to verify runtime services, node-plugin instances, subscription content, and client connectivity.

## Operations

The `.data` directory contains the primary database, hot-event database, instance encryption key, plugin artifacts, plugin state, and event archives. Back up and restore the complete directory as one unit.

See [Operations](docs/operations.md) for backup, development-version replacement, rollback, administrator recovery, instance-key recovery, and node credential recovery procedures. A current `v0.x` data directory can be restored only with the exact image version that created it; it cannot be reused by another version.

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
go run ./cmd/relayward serve -listen 127.0.0.1:8080 -data ./data

cd web
npm run dev
```

The Vite server proxies API requests to `127.0.0.1:8080`. Relayward selects session-cookie security attributes from the browser request protocol.

## Related Projects

- [Relayward Agent](https://github.com/Relayward/relayward-agent)
- [Relayward SDK](https://github.com/Relayward/relayward-sdk)
- [Relayward Xray Plugin](https://github.com/Relayward/relayward-plugin-xray)

## Security

Treat plugins as trusted native executables and install them only from repositories you trust. HTTP is fully functional but provides no transport encryption; use HTTPS for untrusted networks and public deployments. Do not publish registration tokens, subscription links, recovery codes, GitHub tokens, private configuration, or Relayward `.data` contents in issue reports.

Report security issues privately through the repository's [GitHub Security Advisories](https://github.com/Relayward/relayward/security/advisories/new).

## License

Relayward is licensed under the [Apache License 2.0](LICENSE).
