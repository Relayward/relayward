package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplacePluginServicesIsAtomicAndPrunesRemovedBindings(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "Edge", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateUser(ctx, User{ID: "user-id", DisplayName: "Alice"}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAuthorization(ctx, Authorization{
		ID: "authorization-id", UserID: "user-id", NodeID: "node-id", Enabled: true,
		ResetKind: "never", Timezone: "UTC", ActivityWindowSeconds: 600, BlockDurationSeconds: 1800,
		SubscriptionTokenHash: make([]byte, 32),
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
INSERT INTO plugin_installations(
    plugin_id, repository, kind, desired_version, active_version, manifest_json,
    permissions_json, state, created_at, updated_at
) VALUES ('io.relayward.test', 'https://github.com/Relayward/test', 'runtime', '1.2.3',
          '1.2.3', '{}', '[]', 'active', ?, ?);
INSERT INTO node_plugin_instances(
    node_id, plugin_id, desired_version, active_version, desired_state, actual_state, updated_at
) VALUES ('node-id', 'io.relayward.test', '1.2.3', '1.2.3', 'running', 'running', ?);`,
		now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	services := []PluginService{
		{ServiceID: "backup", DisplayName: "Backup", Enabled: true, Capabilities: []string{}, SubscriptionSHA256: strings.Repeat("a", 64)},
		{ServiceID: "main", DisplayName: "Main", Enabled: true, Capabilities: []string{}, SubscriptionSHA256: strings.Repeat("b", 64)},
	}
	if err := database.ReplacePluginServices(ctx, "io.relayward.test", "node-id", services, now); err != nil {
		t.Fatal(err)
	}
	for index, service := range services {
		if err := database.CreateServiceBinding(ctx, ServiceBinding{
			ID: "binding-" + service.ServiceID, AuthorizationID: "authorization-id",
			PluginID: "io.relayward.test", ServiceID: service.ServiceID, Enabled: true,
		}, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	services[1].Enabled = false
	if err := database.ReplacePluginServices(ctx, "io.relayward.test", "node-id", services[1:], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	bindings, err := database.ListServiceBindings(ctx, "authorization-id")
	if err != nil || len(bindings) != 1 || bindings[0].ServiceID != "main" {
		t.Fatalf("bindings after replacement = %+v, %v", bindings, err)
	}
	if err := database.RequirePluginService(ctx, "authorization-id", "io.relayward.test", "main"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled service requirement error = %v", err)
	}
	listed, err := database.ListPluginServices(ctx, "node-id")
	if err != nil || len(listed) != 1 || listed[0].PluginID != "io.relayward.test" || listed[0].Enabled {
		t.Fatalf("ListPluginServices() = %+v, %v", listed, err)
	}
}
