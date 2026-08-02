# Relayward Operations

This document covers the supported first-release deployment: one Linux AMD64 control-plane container behind an HTTPS reverse proxy, with persistent state in one Docker volume.

## Install

1. Copy `compose.yaml` and `.env.example` to a deployment directory.
2. Rename `.env.example` to `.env` and pin `RELAYWARD_VERSION` to an explicit release such as `0.1.0`. Keep the default loopback bind unless the reverse proxy reaches Relayward over a private Docker network.
3. Validate and start the deployment:

   ```bash
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose exec relayward relayward healthcheck
   ```

4. Configure the reverse proxy to terminate TLS and forward every path, including WebSocket upgrades, to Relayward. Do not expose the plain HTTP listener to the Internet.
5. Open the HTTPS administration URL, create the single administrator, and store the generated recovery codes outside the Relayward data volume.

The center image is Linux AMD64 only. The container runs without root privileges, with a read-only root filesystem, dropped capabilities, `no-new-privileges`, and a PID limit. Plugins are administrator-approved native executables and share the Relayward account; the process and RPC limits reduce accidental resource exhaustion but are not a hostile-code sandbox.

## State And Backup

The `relayward-data` volume is the complete recovery unit. It contains both SQLite databases, the instance encryption key, plugin artifacts, plugin state, and event archives. Never back up only a database file or only the instance key.

For a portable backup, stop the service and archive the whole mounted volume with the host's volume or snapshot tooling:

```bash
docker compose stop relayward
# Create a point-in-time archive or storage snapshot of the relayward-data volume.
docker compose start relayward
docker compose exec relayward relayward healthcheck
```

A storage snapshot may be taken without a long stop only when it preserves a crash-consistent view of the entire volume. Keep at least one tested backup outside the host.

Restore the complete volume into an empty deployment, pin the center image to the version that created the backup, start Relayward, and run the health check before changing versions. Do not merge files from different backup points.

## Upgrade

1. Read the release notes and retain the currently running image version.
2. Create a point-in-time backup of `relayward-data`. Database migrations are forward-only.
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
2. Restore the complete pre-upgrade `relayward-data` backup.
3. Set `RELAYWARD_VERSION` to the matching previous release.
4. Start Relayward and verify the health endpoint, secret availability, administrator login, and Agent reconnections.

Do not run an older binary against a data directory already migrated by a newer release.

## Lost Or Damaged Instance Key

Relayward does not silently replace a missing or damaged instance key when encrypted records exist. The service remains available in a degraded state for data that does not require decryption. TOTP and private GitHub tokens remain unavailable until recovered or reset.

Preferred recovery is to restore the complete data volume, including the original instance key. If no valid backup exists, the remaining ciphertext cannot be recovered. The local recovery command intentionally discards it while preserving ordinary business and telemetry data:

1. Stop the running center so it cannot retain the old secret-manager state.
2. Run the one-off recovery command against the same data volume:

   ```bash
   docker compose stop relayward
   docker compose run --rm --no-deps relayward \
     admin recover-secrets -data /var/lib/relayward \
     -confirm-discard-encrypted-secrets
   docker compose up -d
   ```

3. Sign in with the administrator password. TOTP, recovery codes, sessions, saved private GitHub tokens, stored node-plugin configurations, and pending encrypted plugin commands have been discarded.
4. Enable TOTP again, store the replacement recovery codes externally, replace private GitHub tokens from the key action in the Plugins view, and submit configuration again for every plugin instance marked failed. Nodes keep their last successfully applied runtime state until a replacement configuration is submitted.
5. Verify the system information reports encrypted secrets as available.

Node, user, authorization, traffic, event, and audit data are not deleted by this reset. Tokens and credentials stored only as hashes do not depend on the instance key.

## Node Credential Recovery

Revoking a node credential immediately disconnects that Agent and prevents reconnection. Create a new one-time registration token for the same node, then rerun the Agent installer or enrollment flow on that node. Registration tokens are short-lived and single-use; do not store them in shell history or deployment files.
