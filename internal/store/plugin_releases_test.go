package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relayward/relayward-sdk/manifest"
)

func TestCommitPluginReleaseStoresApprovedStateAndEncryptedToken(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 14, 0, 0, 0, time.UTC)
	pluginManifest := testRuntimeManifest()
	pluginManifest.Permissions = []manifest.Permission{{Name: "core.nodes.read", Reason: "Read node status."}}
	version := pluginVersionFixture(pluginManifest, 10, 2, 3, 4)
	installation := PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest,
	}
	created, err := database.CommitPluginRelease(ctx, installation, version, []byte("encrypted-token"), now)
	if err != nil {
		t.Fatalf("CommitPluginRelease() error = %v", err)
	}
	if created.ActiveVersion != pluginManifest.Version || created.Health != "healthy" ||
		len(created.ApprovedPermissions) != 1 || created.ApprovedPermissions[0] != "core.nodes.read" {
		t.Fatalf("created installation = %+v", created)
	}
	if created.LastStartedAt == nil || !created.LastStartedAt.Equal(now) {
		t.Fatalf("created last started at = %v", created.LastStartedAt)
	}
	failedAt := now.Add(30 * time.Second)
	if err := database.RecordPluginRuntimeStatus(ctx, pluginManifest.ID, "failed", "unhealthy", 2, nil, failedAt); err != nil {
		t.Fatal(err)
	}
	failed, err := database.PluginInstallationByID(ctx, pluginManifest.ID)
	if err != nil || failed.LastStartedAt == nil || !failed.LastStartedAt.Equal(now) {
		t.Fatalf("failed installation last started at = %+v, %v", failed, err)
	}
	restartedAt := now.Add(time.Minute)
	if err := database.RecordPluginRuntimeStatus(ctx, pluginManifest.ID, "active", "healthy", 2, nil, restartedAt); err != nil {
		t.Fatal(err)
	}
	restarted, err := database.PluginInstallationByID(ctx, pluginManifest.ID)
	if err != nil || restarted.LastStartedAt == nil || !restarted.LastStartedAt.Equal(restartedAt) {
		t.Fatalf("restarted installation last started at = %+v, %v", restarted, err)
	}
	storedVersion, err := database.PluginVersionByID(ctx, pluginManifest.ID, pluginManifest.Version)
	if err != nil || storedVersion.ReleaseID != 10 || storedVersion.NodeAssetID == nil || *storedVersion.NodeAssetID != 3 {
		t.Fatalf("stored version = %+v, %v", storedVersion, err)
	}
	secret, err := database.Secret(ctx, PluginInstallationSecretOwnerType, pluginManifest.ID, PluginInstallationGitHubToken)
	if err != nil || string(secret) != "encrypted-token" {
		t.Fatalf("stored GitHub token = %q, %v", secret, err)
	}
	audit, err := database.ListAudit(ctx, 0, 10)
	if err != nil || len(audit) != 1 || audit[0].Action != "plugin.install" {
		t.Fatalf("audit = %+v, %v", audit, err)
	}
	if encoded := strings.ToLower(strings.TrimSpace(toJSON(t, audit[0].Metadata))); strings.Contains(encoded, "token") {
		t.Fatalf("audit metadata contains token material: %s", encoded)
	}

	upgradedManifest := pluginManifest
	upgradedManifest.Version = "1.2.4"
	upgraded := pluginVersionFixture(upgradedManifest, 11, 5, 6, 7)
	installation.DesiredVersion = upgradedManifest.Version
	installation.ActiveVersion = upgradedManifest.Version
	installation.Manifest = upgradedManifest
	updated, err := database.CommitPluginRelease(ctx, installation, upgraded, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CommitPluginRelease() upgrade error = %v", err)
	}
	if updated.ActiveVersion != "1.2.4" || updated.PreviousVersion != "1.2.3" {
		t.Fatalf("updated installation = %+v", updated)
	}
	if retained, err := database.Secret(ctx, PluginInstallationSecretOwnerType, pluginManifest.ID, PluginInstallationGitHubToken); err != nil || string(retained) != "encrypted-token" {
		t.Fatalf("retained GitHub token = %q, %v", retained, err)
	}
	versions, err := database.ListPluginVersions(ctx, pluginManifest.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("plugin versions = %+v, %v", versions, err)
	}
}

func TestPluginUninstallRequiresAbsentNodeInstancesAndDeletesSecrets(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	pluginManifest := testRuntimeManifest()
	version := pluginVersionFixture(pluginManifest, 10, 2, 3, 4)
	installation := PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest,
	}
	if _, err := database.CommitPluginRelease(ctx, installation, version, []byte("ciphertext"), now); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
INSERT INTO node_plugin_instances(node_id, plugin_id, desired_version, active_version, desired_state, actual_state, updated_at)
VALUES ('node-id', ?, ?, ?, 'running', 'running', ?)`, pluginManifest.ID, pluginManifest.Version, pluginManifest.Version, unixTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DeletePluginInstallation(ctx, pluginManifest.ID, now); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("DeletePluginInstallation() active node error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `
UPDATE node_plugin_instances SET desired_state = 'absent', actual_state = 'absent' WHERE plugin_id = ?`, pluginManifest.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := database.DeletePluginInstallation(ctx, pluginManifest.ID, now.Add(time.Minute))
	if err != nil || len(removed) != 1 {
		t.Fatalf("DeletePluginInstallation() = %+v, %v", removed, err)
	}
	if _, err := database.Secret(ctx, PluginInstallationSecretOwnerType, pluginManifest.ID, PluginInstallationGitHubToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GitHub token after uninstall error = %v", err)
	}
}

func pluginVersionFixture(value manifest.Manifest, releaseID, centerID, nodeID, uiID int64) PluginVersion {
	approved := make([]string, len(value.Permissions))
	for index, permission := range value.Permissions {
		approved[index] = permission.Name
	}
	result := PluginVersion{
		PluginID: value.ID, Version: value.Version, ReleaseID: releaseID, ReleaseTag: "v" + value.Version,
		Manifest: value, ApprovedPermissions: approved, CenterAssetID: centerID,
	}
	for _, artifact := range value.Artifacts {
		switch artifact.Role {
		case manifest.ArtifactNode:
			id := nodeID
			result.NodeAssetID = &id
		case manifest.ArtifactUI:
			id := uiID
			result.UIAssetID = &id
		}
	}
	return result
}

func toJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
