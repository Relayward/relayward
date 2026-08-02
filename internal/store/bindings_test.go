package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceBindingLifecycleRequiresNodePluginService(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "node", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := database.CreateUser(ctx, User{ID: "user-id", DisplayName: "user"}, now); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := database.CreateAuthorization(ctx, Authorization{
		ID: "authorization-id", UserID: "user-id", NodeID: "node-id", Enabled: true,
		ResetKind: "never", Timezone: "UTC", ActivityWindowSeconds: 600,
		BlockDurationSeconds: 1800, SubscriptionTokenHash: make([]byte, 32),
	}, now); err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}

	missing := ServiceBinding{
		ID: "missing-binding-id", AuthorizationID: "authorization-id",
		PluginID: "test-runtime", ServiceID: "test-service", Enabled: true,
	}
	if err := database.CreateServiceBinding(ctx, missing, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateServiceBinding() without plugin service error = %v", err)
	}

	if _, err := database.db.ExecContext(ctx, `
INSERT INTO plugin_installations(
    plugin_id, repository, kind, desired_version, active_version, manifest_json,
    permissions_json, state, created_at, updated_at
) VALUES ('test-runtime', 'https://github.com/Relayward/test-runtime', 'runtime', 'v0.1.0',
          'v0.1.0', '{}', '[]', 'active', ?, ?);
INSERT INTO node_plugin_instances(
    node_id, plugin_id, desired_version, active_version, desired_state, actual_state, updated_at
) VALUES ('node-id', 'test-runtime', 'v0.1.0', 'v0.1.0', 'running', 'running', ?);
INSERT INTO plugin_services(
    node_id, plugin_id, service_id, display_name, enabled, capabilities_json, updated_at
) VALUES ('node-id', 'test-runtime', 'test-service', 'Test service', 1, '{}', ?);`,
		unixTime(now), unixTime(now), unixTime(now), unixTime(now)); err != nil {
		t.Fatalf("insert test plugin service: %v", err)
	}

	binding := ServiceBinding{
		ID: "binding-id", AuthorizationID: "authorization-id",
		PluginID: "test-runtime", ServiceID: "test-service", Enabled: true,
	}
	if err := database.CreateServiceBinding(ctx, binding, now); err != nil {
		t.Fatalf("CreateServiceBinding() error = %v", err)
	}
	created, err := database.ServiceBindingByID(ctx, binding.ID)
	if err != nil || !created.Enabled || created.PluginID != binding.PluginID || created.ServiceID != binding.ServiceID {
		t.Fatalf("ServiceBindingByID() = %+v, %v", created, err)
	}

	duplicate := binding
	duplicate.ID = "duplicate-binding-id"
	if err := database.CreateServiceBinding(ctx, duplicate, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateServiceBinding() error = %v", err)
	}
	if err := database.UpdateServiceBinding(ctx, binding.ID, false, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateServiceBinding() error = %v", err)
	}
	updated, err := database.ServiceBindingByID(ctx, binding.ID)
	if err != nil || updated.Enabled {
		t.Fatalf("ServiceBindingByID() after update = %+v, %v", updated, err)
	}
	if err := database.DeleteServiceBinding(ctx, binding.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("DeleteServiceBinding() error = %v", err)
	}
	if _, err := database.ServiceBindingByID(ctx, binding.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ServiceBindingByID() after delete error = %v", err)
	}
}
