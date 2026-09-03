package store

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE administrators (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1)),
    totp_last_counter INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    id TEXT NOT NULL DEFAULT '',
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    administrator_id INTEGER NOT NULL REFERENCES administrators(id) ON DELETE CASCADE,
    user_agent TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);
CREATE UNIQUE INDEX sessions_id_idx ON sessions(id);

CREATE TABLE recovery_codes (
    administrator_id INTEGER NOT NULL REFERENCES administrators(id) ON DELETE CASCADE,
    code_hash BLOB NOT NULL CHECK (length(code_hash) = 32),
    created_at INTEGER NOT NULL,
    used_at INTEGER,
    PRIMARY KEY (administrator_id, code_hash)
);

CREATE TABLE secrets (
    owner_type TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    name TEXT NOT NULL,
    ciphertext BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (owner_type, owner_id, name)
);

CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    credential_hash BLOB CHECK (credential_hash IS NULL OR length(credential_hash) = 32),
    registered_at INTEGER,
    last_seen_at INTEGER,
    hostname TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    agent_os TEXT NOT NULL DEFAULT '',
    agent_arch TEXT NOT NULL DEFAULT '',
    agent_capabilities_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(agent_capabilities_json)),
    agent_started_at_ns INTEGER,
    registration_count INTEGER NOT NULL DEFAULT 0 CHECK (registration_count >= 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE node_registration_tokens (
    token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER
);
CREATE INDEX node_registration_tokens_expiry_idx ON node_registration_tokens(expires_at);

CREATE TABLE node_public_addresses (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    family TEXT NOT NULL CHECK (family IN ('ipv4', 'ipv6')),
    address TEXT NOT NULL,
    observed_at_ns INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, family)
);

CREATE TABLE dns_provider_connections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    provider TEXT NOT NULL CHECK (provider IN ('cloudflare')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE node_endpoints (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    display_name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('direct', 'nat', 'domain', 'managed_ddns')),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    source_family TEXT NOT NULL DEFAULT '' CHECK (source_family IN ('', 'ipv4', 'ipv6')),
    address TEXT NOT NULL DEFAULT '',
    public_port_overrides_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(public_port_overrides_json)),
    dns_provider_connection_id TEXT REFERENCES dns_provider_connections(id) ON DELETE RESTRICT,
    zone_name TEXT NOT NULL DEFAULT '',
    record_name TEXT NOT NULL DEFAULT '',
    ttl INTEGER NOT NULL DEFAULT 1 CHECK (ttl = 1 OR ttl BETWEEN 60 AND 86400),
    proxied INTEGER NOT NULL DEFAULT 0 CHECK (proxied IN (0, 1)),
    sync_status TEXT NOT NULL DEFAULT 'not_applicable'
        CHECK (sync_status IN ('not_applicable', 'pending', 'synced', 'failed')),
    actual_address TEXT NOT NULL DEFAULT '',
    sync_error TEXT NOT NULL DEFAULT '',
    synced_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (node_id, display_name COLLATE NOCASE),
    CHECK (
        (kind = 'direct' AND source_family IN ('ipv4', 'ipv6') AND address = ''
            AND dns_provider_connection_id IS NULL AND zone_name = '' AND record_name = '' AND sync_status = 'not_applicable')
        OR (kind = 'nat' AND source_family = '' AND address <> ''
            AND dns_provider_connection_id IS NULL AND zone_name = '' AND record_name = '' AND sync_status = 'not_applicable')
        OR (kind = 'domain' AND source_family = '' AND address <> ''
            AND dns_provider_connection_id IS NULL AND zone_name = '' AND record_name = '' AND sync_status = 'not_applicable')
        OR (kind = 'managed_ddns' AND source_family IN ('ipv4', 'ipv6') AND address = ''
            AND dns_provider_connection_id IS NOT NULL AND zone_name <> '' AND record_name <> '' AND sync_status <> 'not_applicable')
    )
);
CREATE INDEX node_endpoints_node_idx ON node_endpoints(node_id, enabled, id);
CREATE INDEX node_endpoints_ddns_idx ON node_endpoints(kind, enabled, sync_status);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email TEXT,
    telegram TEXT,
    note TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE authorizations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    traffic_limit_bytes INTEGER CHECK (traffic_limit_bytes IS NULL OR traffic_limit_bytes >= 0),
    reset_kind TEXT NOT NULL DEFAULT 'never'
        CHECK (reset_kind IN ('never', 'daily', 'weekly', 'monthly', 'interval_days')),
    reset_value INTEGER,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    period_anchor INTEGER,
    expires_at INTEGER,
    soft_ip_limit INTEGER CHECK (soft_ip_limit IS NULL OR soft_ip_limit > 0),
    activity_window_seconds INTEGER NOT NULL DEFAULT 600 CHECK (activity_window_seconds > 0),
    block_duration_seconds INTEGER NOT NULL DEFAULT 1800 CHECK (block_duration_seconds > 0),
    subscription_token_hash BLOB NOT NULL UNIQUE CHECK (length(subscription_token_hash) = 32),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (user_id, node_id)
);

CREATE TABLE plugin_installations (
    plugin_id TEXT PRIMARY KEY,
    repository TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('runtime', 'feature')),
    desired_version TEXT NOT NULL,
    active_version TEXT,
    previous_version TEXT,
    release_id INTEGER NOT NULL DEFAULT 0 CHECK (release_id >= 0),
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    permissions_json TEXT NOT NULL CHECK (json_valid(permissions_json)),
    approved_permissions_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(approved_permissions_json)),
    state TEXT NOT NULL,
    health TEXT NOT NULL DEFAULT 'unknown',
    restart_count INTEGER NOT NULL DEFAULT 0 CHECK (restart_count >= 0),
    last_problem_json TEXT CHECK (last_problem_json IS NULL OR json_valid(last_problem_json)),
    last_started_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE plugin_versions (
    plugin_id TEXT NOT NULL REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE,
    version TEXT NOT NULL,
    release_id INTEGER NOT NULL CHECK (release_id > 0),
    release_tag TEXT NOT NULL,
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    approved_permissions_json TEXT NOT NULL CHECK (json_valid(approved_permissions_json)),
    center_asset_id INTEGER NOT NULL CHECK (center_asset_id > 0),
    node_asset_id INTEGER CHECK (node_asset_id IS NULL OR node_asset_id > 0),
    ui_asset_id INTEGER CHECK (ui_asset_id IS NULL OR ui_asset_id > 0),
    installed_at INTEGER NOT NULL,
    PRIMARY KEY (plugin_id, version)
);

CREATE TABLE node_plugin_instances (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    plugin_id TEXT NOT NULL REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE,
    desired_version TEXT NOT NULL,
    active_version TEXT,
    desired_state TEXT NOT NULL,
    actual_state TEXT NOT NULL,
    generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0),
    desired_configuration_sha256 TEXT NOT NULL DEFAULT '',
    artifact_size INTEGER NOT NULL DEFAULT 0 CHECK (artifact_size >= 0),
    artifact_sha256 TEXT NOT NULL DEFAULT '',
    actual_generation INTEGER NOT NULL DEFAULT 0 CHECK (actual_generation >= 0),
    actual_configuration_sha256 TEXT NOT NULL DEFAULT '',
    health TEXT NOT NULL DEFAULT 'unknown',
    reason TEXT NOT NULL DEFAULT '',
    restart_count INTEGER NOT NULL DEFAULT 0 CHECK (restart_count >= 0),
    reconcile_status TEXT NOT NULL DEFAULT 'pending',
    last_problem_json TEXT CHECK (last_problem_json IS NULL OR json_valid(last_problem_json)),
    last_command_id TEXT,
    actual_observed_at_ns INTEGER,
    capabilities_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities_json)),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, plugin_id)
);
CREATE INDEX node_plugin_instances_plugin_idx ON node_plugin_instances(plugin_id, node_id);

CREATE TABLE plugin_services (
    node_id TEXT NOT NULL,
    plugin_id TEXT NOT NULL,
    service_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    capabilities_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities_json)),
    subscription_sha256 TEXT NOT NULL DEFAULT ''
        CHECK (subscription_sha256 = '' OR length(subscription_sha256) = 64),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, plugin_id, service_id),
    FOREIGN KEY (node_id, plugin_id)
        REFERENCES node_plugin_instances(node_id, plugin_id) ON DELETE CASCADE
);

CREATE TABLE service_bindings (
    id TEXT PRIMARY KEY,
    authorization_id TEXT NOT NULL REFERENCES authorizations(id) ON DELETE CASCADE,
    plugin_id TEXT NOT NULL,
    service_id TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (authorization_id, plugin_id, service_id)
);

CREATE TABLE agent_commands (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    request_json TEXT NOT NULL CHECK (json_valid(request_json)),
    request_sha256 TEXT NOT NULL CHECK (length(request_sha256) = 64),
    request_encrypted INTEGER NOT NULL DEFAULT 0 CHECK (request_encrypted IN (0, 1)),
    scope_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'succeeded', 'failed', 'expired')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_sent_at INTEGER,
    result_json TEXT CHECK (result_json IS NULL OR json_valid(result_json)),
    completed_at INTEGER,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX agent_commands_dispatch_idx ON agent_commands(node_id, status, created_at, id);
CREATE UNIQUE INDEX agent_commands_one_pending_update_idx
ON agent_commands(node_id)
WHERE kind = 'agent.update' AND status = 'pending';
CREATE UNIQUE INDEX agent_commands_one_pending_plugin_reconcile_idx
ON agent_commands(node_id, kind, scope_key)
WHERE kind = 'plugin.reconcile' AND status = 'pending';
CREATE UNIQUE INDEX agent_commands_one_pending_policy_reconcile_idx
ON agent_commands(node_id)
WHERE kind = 'policy.reconcile' AND status = 'pending';

CREATE TABLE node_policy_state (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    desired_generation INTEGER NOT NULL DEFAULT 0 CHECK (desired_generation >= 0),
    desired_sha256 TEXT NOT NULL DEFAULT '' CHECK (desired_sha256 = '' OR length(desired_sha256) = 64),
    applied_generation INTEGER NOT NULL DEFAULT 0 CHECK (applied_generation >= 0),
    reconcile_status TEXT NOT NULL DEFAULT 'not_configured'
        CHECK (reconcile_status IN ('not_configured', 'pending', 'applied', 'failed', 'unsupported')),
    last_problem_json TEXT CHECK (last_problem_json IS NULL OR json_valid(last_problem_json)),
    last_command_id TEXT,
    issued_agent_started_at_ns INTEGER,
    retry_after INTEGER,
    updated_at INTEGER NOT NULL
);

CREATE TABLE traffic_periods (
    authorization_id TEXT NOT NULL REFERENCES authorizations(id) ON DELETE CASCADE,
    period_id TEXT NOT NULL,
    starts_at INTEGER NOT NULL,
    ends_at INTEGER,
    upload_bytes INTEGER NOT NULL DEFAULT 0 CHECK (upload_bytes >= 0),
    download_bytes INTEGER NOT NULL DEFAULT 0 CHECK (download_bytes >= 0),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    enforced_at INTEGER,
    observed_at_ns INTEGER,
    source_stream_id TEXT NOT NULL DEFAULT '',
    source_sequence INTEGER NOT NULL DEFAULT 0 CHECK (source_sequence >= 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (authorization_id, period_id)
);

CREATE TABLE authorization_policy_status (
    authorization_id TEXT PRIMARY KEY REFERENCES authorizations(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    period_id TEXT NOT NULL CHECK (length(period_id) = 32),
    starts_at INTEGER NOT NULL,
    ends_at INTEGER,
    upload_bytes INTEGER NOT NULL CHECK (upload_bytes >= 0),
    download_bytes INTEGER NOT NULL CHECK (download_bytes >= 0),
    services_enabled INTEGER NOT NULL CHECK (services_enabled IN (0, 1)),
    reason TEXT NOT NULL
        CHECK (reason IN ('active', 'administrator_disabled', 'expired', 'quota_exceeded')),
    active_ip_count INTEGER NOT NULL CHECK (active_ip_count >= 0),
    blocked_ip_count INTEGER NOT NULL CHECK (blocked_ip_count >= 0),
    observed_at_ns INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE announcements (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    content TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE TABLE subscription_render_cache (
    authorization_id TEXT NOT NULL REFERENCES authorizations(id) ON DELETE CASCADE,
    format TEXT NOT NULL,
    content BLOB NOT NULL,
    input_sha256 TEXT NOT NULL DEFAULT ''
        CHECK (input_sha256 = '' OR length(input_sha256) = 64),
    rendered_at INTEGER NOT NULL,
    PRIMARY KEY (authorization_id, format)
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at INTEGER NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json))
);
CREATE INDEX audit_log_occurred_idx ON audit_log(occurred_at DESC, id DESC);

CREATE TABLE system_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    session_lifetime_minutes INTEGER NOT NULL DEFAULT 1440
        CHECK (session_lifetime_minutes BETWEEN 60 AND 525600),
    timezone TEXT NOT NULL DEFAULT 'UTC',
    public_url TEXT NOT NULL DEFAULT '',
    subscription_title TEXT NOT NULL DEFAULT 'Relayward',
    support_url TEXT NOT NULL DEFAULT '',
    profile_url TEXT NOT NULL DEFAULT '',
    subscription_refresh_hours INTEGER NOT NULL DEFAULT 12
        CHECK (subscription_refresh_hours BETWEEN 0 AND 8760),
    updated_at INTEGER NOT NULL
);
INSERT INTO system_settings(id, updated_at) VALUES (1, unixepoch());

CREATE TRIGGER agent_command_secret_cleanup
AFTER DELETE ON agent_commands
BEGIN
    DELETE FROM secrets
    WHERE owner_type = 'agent_command' AND owner_id = OLD.id AND name = 'request';
END;

CREATE TRIGGER node_plugin_secret_cleanup
AFTER DELETE ON node_plugin_instances
BEGIN
    DELETE FROM secrets
    WHERE owner_type = 'node_plugin_instance'
      AND owner_id = OLD.node_id || '/' || OLD.plugin_id
      AND name = 'desired_configuration';
END;

CREATE TRIGGER plugin_installation_secret_cleanup
AFTER DELETE ON plugin_installations
BEGIN
    DELETE FROM secrets
    WHERE owner_type = 'plugin_installation' AND owner_id = OLD.plugin_id;
END;

CREATE TRIGGER dns_provider_connection_secret_cleanup
AFTER DELETE ON dns_provider_connections
BEGIN
    DELETE FROM secrets
    WHERE owner_type = 'dns_provider_connection' AND owner_id = OLD.id;
END;
`,
	},
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	for _, item := range migrations {
		var applied int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", item.version).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read migration %d: %w", item.version, err)
		}
		if err := applyMigration(ctx, db, item); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, item migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", item.version, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, item.sql); err != nil {
		return fmt.Errorf("apply migration %d: %w", item.version, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch())", item.version); err != nil {
		return fmt.Errorf("record migration %d: %w", item.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", item.version, err)
	}
	return nil
}
