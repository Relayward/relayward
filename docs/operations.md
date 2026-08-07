# Relayward Operations

This document covers the supported first-release deployment: one Linux AMD64 control-plane container with persistent state in `.data` under the deployment directory. The center may be reached directly over HTTP or through an HTTPS reverse proxy.

## Install

1. Enter an empty deployment directory of your choice and download the repository's deployment examples:

   ```bash
   curl -fsSL \
     https://raw.githubusercontent.com/Relayward/relayward/main/compose.yaml \
     -o compose.yaml
   curl -fsSL \
     https://raw.githubusercontent.com/Relayward/relayward/main/.env.example \
     -o .env
   ```

2. Review `.env` and keep `RELAYWARD_VERSION` pinned to an explicit release such as `0.2.2`. Keep the default loopback bind when using a host reverse proxy. For direct HTTP access, set `RELAYWARD_BIND_ADDRESS` to a host address reachable by administrators and Agents. On first start, the container creates `.data` and prepares its permissions automatically while the Relayward process continues to run as a non-root user.
3. Validate, pull, and start the deployment:

   ```bash
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose exec relayward relayward healthcheck
   ```

4. Choose the access method. Direct HTTP is supported, but administrator credentials, sessions, subscription tokens, Agent registration credentials, commands, and telemetry are not encrypted in transit. An HTTPS reverse proxy is strongly recommended for untrusted networks and public deployments. When using a proxy, forward every path including WebSocket upgrades, preserve `Host`, and pass `X-Forwarded-Proto`.
5. Open the selected HTTP or HTTPS administration URL and create the single administrator. TOTP is optional but strongly recommended on public or untrusted networks. If enabled, store its generated recovery codes outside the Relayward `.data` directory.

The center image is Linux AMD64 only. Its entrypoint uses root only to prepare the mounted data directory, then Relayward and its plugins run as UID `10001`. Plugins are administrator-approved native executables and share the Relayward account; plugin process isolation is not a hostile-code sandbox.

## State And Backup

The `.data` directory is the complete recovery unit. It contains both SQLite databases, the instance encryption key, plugin artifacts, plugin state, and event archives. Never back up only a database file or only the instance key.

For a portable backup, stop the service and archive the complete `.data` directory with the host's archive or snapshot tooling:

```bash
docker compose stop relayward
# Create a point-in-time archive or storage snapshot of .data.
docker compose start relayward
docker compose exec relayward relayward healthcheck
```

A storage snapshot may be taken without a long stop only when it preserves a crash-consistent view of the entire volume. Keep at least one tested backup outside the host.

Restore the complete `.data` directory into an empty deployment, pin the center image to the version that created the backup, start Relayward, and run the health check before changing versions. Do not merge files from different backup points.

## Upgrade

1. Read the release notes and retain the currently running image version.
2. Create a point-in-time backup of `.data`. Database migrations are forward-only.
3. Change `RELAYWARD_VERSION` to the exact target release.
4. Pull and recreate the service:

   ```bash
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose exec relayward relayward healthcheck
   docker compose logs --tail=100 relayward
   ```

5. Confirm that the administration UI reports encrypted secrets as available and that expected Agents reconnect.

Plugin upgrades are separate administrator actions. Relayward verifies Release metadata and SHA-256 values, starts the candidate with its approved permissions, and commits it only after health checks pass. A failed candidate restores the previous center plugin. Failed upgrades and automatic rollback outcomes are recorded in the audit log.

## Rollback

Changing only the image tag is safe only when no forward database migration ran. For a released upgrade, assume a migration may have run:

1. Stop Relayward.
2. Restore the complete pre-upgrade `.data` backup.
3. Set `RELAYWARD_VERSION` to the matching previous release.
4. Start Relayward and verify the health endpoint, secret availability, administrator login, and Agent reconnections.

Do not run an older binary against a data directory already migrated by a newer release.

## Lost Or Damaged Instance Key

Relayward does not silently replace a missing or damaged instance key when encrypted records exist. The service remains available in a degraded state for data that does not require decryption. TOTP and private GitHub tokens remain unavailable until recovered or reset.

Preferred recovery is to restore the complete `.data` directory, including the original instance key. If no valid backup exists, the remaining ciphertext cannot be recovered. The local recovery command intentionally discards it while preserving ordinary business and telemetry data:

1. Stop the running center so it cannot retain the old secret-manager state.
2. Run the one-off recovery command against the same data directory:

   ```bash
   docker compose stop relayward
   docker compose run --rm --no-deps relayward \
     admin recover-secrets -data /var/lib/relayward \
     -confirm-discard-encrypted-secrets
   docker compose up -d
   ```

3. Sign in with the administrator password. TOTP, recovery codes, sessions, saved private GitHub tokens, stored node-plugin configurations, and pending encrypted plugin commands have been discarded.
4. If TOTP was in use, enable it again and store the replacement recovery codes externally. Replace private GitHub tokens from the key action in the Plugins view, and submit configuration again for every plugin instance marked failed. Nodes keep their last successfully applied runtime state until a replacement configuration is submitted.
5. Verify the system information reports encrypted secrets as available.

Node, user, authorization, traffic, event, and audit data are not deleted by this reset. Tokens and credentials stored only as hashes do not depend on the instance key.

## Administrator Password Recovery

If the administrator password is lost, reset it locally against the persistent `.data` directory. Stop the center first so no running process can continue using stale session state. The new password must contain at least 12 characters and is read from standard input so it does not appear in the process list or shell history.

```bash
docker compose stop relayward
read -rsp 'New administrator password: ' RELAYWARD_PASSWORD
printf '\n'
printf '%s\n' "$RELAYWARD_PASSWORD" | docker compose run --rm --no-deps -T relayward \
  admin reset-password -data /var/lib/relayward -password-stdin
unset RELAYWARD_PASSWORD
docker compose up -d
docker compose exec relayward relayward healthcheck
```

The reset revokes every administrator session. Sign in with the new password after the service starts. If TOTP was enabled, it remains enabled and still requires the existing authenticator or a recovery code.

## Node Credential Recovery

Revoking a node credential immediately disconnects that Agent and prevents reconnection. Create a new one-time registration token for the same node, then rerun the Agent installer or enrollment flow on that node. Registration tokens are short-lived and single-use; do not store them in shell history or deployment files.
