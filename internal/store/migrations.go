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
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    administrator_id INTEGER NOT NULL REFERENCES administrators(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expires_at);

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
    public_address TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    credential_hash BLOB CHECK (credential_hash IS NULL OR length(credential_hash) = 32),
    registered_at INTEGER,
    last_seen_at INTEGER,
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
    reset_kind TEXT NOT NULL DEFAULT 'never' CHECK (reset_kind IN ('never', 'daily', 'weekly', 'monthly', 'interval_days')),
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

CREATE TABLE plugin_installations (
    plugin_id TEXT PRIMARY KEY,
    repository TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('runtime', 'feature')),
    desired_version TEXT NOT NULL,
    active_version TEXT,
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    permissions_json TEXT NOT NULL CHECK (json_valid(permissions_json)),
    state TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE node_plugin_instances (
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    plugin_id TEXT NOT NULL REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE,
    desired_version TEXT NOT NULL,
    active_version TEXT,
    desired_state TEXT NOT NULL,
    actual_state TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, plugin_id)
);

CREATE TABLE traffic_periods (
    authorization_id TEXT NOT NULL REFERENCES authorizations(id) ON DELETE CASCADE,
    period_id TEXT NOT NULL,
    starts_at INTEGER NOT NULL,
    ends_at INTEGER,
    upload_bytes INTEGER NOT NULL DEFAULT 0 CHECK (upload_bytes >= 0),
    download_bytes INTEGER NOT NULL DEFAULT 0 CHECK (download_bytes >= 0),
    enforced_at INTEGER,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (authorization_id, period_id)
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
