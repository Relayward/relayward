# Relayward Control Plane AGENTS.md

## Project Role

This repository contains the Relayward control plane, administration interface, and subscription pages. It owns administrator authentication, nodes, users, node authorizations, quotas, subscriptions, audit records, plugin lifecycle, and normalized telemetry contracts.

The control plane must never run or package a local proxy core. Proxy-core-specific configuration and lifecycle behavior belongs in runtime plugins.

## Architecture

- Use Go for the API, background jobs, Agent sessions, and plugin supervision.
- Use React and TypeScript for the administration interface and subscription pages.
- Use SQLite for the single-instance business database and a separate SQLite database for hot events.
- Keep plugin processes isolated behind the versioned contracts from `Relayward/relayward-sdk`.
- Do not add Redis, PostgreSQL, a message broker, microservices, or multi-instance coordination without an explicit architecture decision.

## Security

- Validate every browser, Agent, plugin, GitHub, and subscription input at its boundary.
- Never log or return passwords, TOTP secrets, GitHub tokens, node credentials, subscription tokens, proxy credentials, source IP data, or complete access events at informational level.
- Keep browser sessions, subscription tokens, node credentials, and plugin credentials separate.
- Plugins must not access the control-plane database directly.

## Engineering Conventions

- Prefer the Go standard library unless a dependency removes substantial complexity.
- Keep HTTP handlers thin and put state transitions behind focused services.
- Keep the frontend operational, compact, and consistent; do not build a marketing landing page.
- Version public Agent and plugin contracts before implementation depends on them.
- Do not add compatibility paths for 3x-ui or the retired xui-stack databases.

## Validation

Run the checks relevant to the change:

- `go test ./...`
- `go vet ./...`
- `go test -race ./...` for concurrency, sessions, jobs, or storage changes
- `go build ./cmd/relayward`
- `npm ci`, `npm run typecheck`, `npm test`, and `npm run build` from `web/` for frontend changes

Cross-repository contract changes must also pass the SDK conformance tests and the affected Agent or plugin consumer tests.
