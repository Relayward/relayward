package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesCurrentBaselineSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relayward.db")
	if len(migrations) != 1 || migrations[0].version != 1 {
		t.Fatalf("migrations = %+v, want one version 1 baseline", migrations)
	}

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	if err := first.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() second error = %v", err)
	}
	defer second.Close()

	var count, minimum, maximum int
	if err := second.db.QueryRowContext(ctx, `
SELECT count(*), min(version), max(version) FROM schema_migrations`).Scan(&count, &minimum, &maximum); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count != 1 || minimum != 1 || maximum != 1 {
		t.Fatalf("migration summary = count %d, min %d, max %d; want only version 1", count, minimum, maximum)
	}
	assertSchemaObjects(t, second.db, "table", []string{
		"administrators", "sessions", "recovery_codes", "secrets", "nodes",
		"node_registration_tokens", "node_public_addresses", "dns_provider_connections", "node_endpoints",
		"users", "authorizations", "plugin_installations",
		"plugin_versions", "node_plugin_instances", "plugin_services", "service_bindings",
		"agent_commands", "node_policy_state", "traffic_periods", "authorization_policy_status",
		"announcements", "subscription_render_cache", "audit_log", "system_settings",
	})
	assertSchemaObjects(t, second.db, "index", []string{
		"sessions_expiry_idx", "sessions_id_idx", "node_registration_tokens_expiry_idx",
		"node_endpoints_node_idx", "node_endpoints_ddns_idx",
		"node_plugin_instances_plugin_idx", "agent_commands_dispatch_idx",
		"agent_commands_one_pending_update_idx", "agent_commands_one_pending_plugin_reconcile_idx",
		"agent_commands_one_pending_policy_reconcile_idx", "audit_log_occurred_idx",
	})
	assertSchemaObjects(t, second.db, "trigger", []string{
		"agent_command_secret_cleanup", "node_plugin_secret_cleanup", "plugin_installation_secret_cleanup",
		"dns_provider_connection_secret_cleanup",
	})
	assertTableColumns(t, second.db, "sessions", []string{"id", "user_agent"})
	assertTableColumns(t, second.db, "nodes", []string{
		"hostname", "agent_version", "agent_os", "agent_arch", "agent_capabilities_json",
		"agent_started_at_ns", "registration_count",
	})
	assertTableColumns(t, second.db, "plugin_installations", []string{
		"approved_permissions_json", "previous_version", "release_id", "health", "restart_count",
		"last_problem_json", "last_started_at",
	})
	assertTableColumns(t, second.db, "node_plugin_instances", []string{
		"generation", "desired_configuration_sha256", "artifact_size", "artifact_sha256",
		"actual_generation", "actual_configuration_sha256", "health", "reason", "restart_count",
		"reconcile_status", "last_problem_json", "last_command_id", "actual_observed_at_ns",
		"capabilities_json",
	})
	assertTableColumns(t, second.db, "plugin_services", []string{"capabilities_json", "subscription_sha256"})
	var capabilitiesDefault string
	if err := second.db.QueryRow(`
SELECT dflt_value FROM pragma_table_info('plugin_services') WHERE name = 'capabilities_json'`).Scan(&capabilitiesDefault); err != nil {
		t.Fatalf("query plugin service capabilities default: %v", err)
	}
	if capabilitiesDefault != "'[]'" {
		t.Errorf("plugin service capabilities default = %q, want '[]'", capabilitiesDefault)
	}
	assertTableColumns(t, second.db, "traffic_periods", []string{
		"revision", "observed_at_ns", "source_stream_id", "source_sequence",
	})
	assertTableColumns(t, second.db, "subscription_render_cache", []string{"input_sha256"})

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("database permissions = %o, want 600", permissions)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open() error = nil, want path error")
	}
}

func TestOpenSerializesReadThenWriteTransactions(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "relayward.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	competingDSN := url.URL{Scheme: "file", Path: path}
	query := competingDSN.Query()
	query.Add("_pragma", "busy_timeout(50)")
	query.Add("_pragma", "journal_mode(WAL)")
	competingDSN.RawQuery = query.Encode()
	competing, err := sql.Open("sqlite", competingDSN.String())
	if err != nil {
		t.Fatalf("open competing connection: %v", err)
	}
	defer competing.Close()
	if err := competing.PingContext(ctx); err != nil {
		t.Fatalf("ping competing connection: %v", err)
	}

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read-then-write transaction: %v", err)
	}
	defer tx.Rollback()
	var users int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("read before write: %v", err)
	}
	if _, err := competing.ExecContext(ctx, `
INSERT INTO users(id, display_name, note, created_at, updated_at)
VALUES ('competing-user', 'Competing user', '', 1, 1)`); err == nil {
		t.Fatal("competing writer was not serialized behind the active transaction")
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO users(id, display_name, note, created_at, updated_at)
VALUES ('primary-user', 'Primary user', '', 1, 1)`); err != nil {
		t.Fatalf("write after read: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit read-then-write transaction: %v", err)
	}

	if err := database.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = 'primary-user'`).Scan(&users); err != nil {
		t.Fatalf("query committed user: %v", err)
	}
	if users != 1 {
		t.Fatalf("committed user count = %d, want 1", users)
	}
}

func assertSchemaObjects(t *testing.T, database *sql.DB, objectType string, names []string) {
	t.Helper()
	for _, name := range names {
		var count int
		if err := database.QueryRow(`
SELECT count(*) FROM sqlite_schema WHERE type = ? AND name = ?`, objectType, name).Scan(&count); err != nil {
			t.Fatalf("query %s %q: %v", objectType, name, err)
		}
		if count != 1 {
			t.Errorf("%s %q count = %d, want 1", objectType, name, count)
		}
	}
}

func assertTableColumns(t *testing.T, database *sql.DB, table string, expected []string) {
	t.Helper()
	rows, err := database.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("query columns for %q: %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column for %q: %v", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %q: %v", table, err)
	}
	for _, name := range expected {
		if _, ok := columns[name]; !ok {
			t.Errorf("table %q is missing column %q", table, name)
		}
	}
}
