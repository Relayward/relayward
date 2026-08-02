package management

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/store"
)

type releaseClientStub struct {
	release            githubrelease.Release
	err                error
	token              string
	resolvedRepository githubrelease.Repository
	resolvedAssetID    int64
	resolvedToken      string
	resolvedURL        string
	resolveErr         error
}

func (client *releaseClientStub) Inspect(_ context.Context, _, _, token string) (githubrelease.Release, error) {
	client.token = token
	return client.release, client.err
}

func (*releaseClientStub) DownloadAsset(context.Context, githubrelease.Repository, githubrelease.Asset, string, string, io.Writer) error {
	return nil
}

func (client *releaseClientStub) ResolveAssetURL(_ context.Context, repository githubrelease.Repository, assetID int64, token string) (string, error) {
	client.resolvedRepository = repository
	client.resolvedAssetID = assetID
	client.resolvedToken = token
	if client.resolveErr != nil {
		return "", client.resolveErr
	}
	if client.resolvedURL != "" {
		return client.resolvedURL, nil
	}
	return "https://release-assets.githubusercontent.com/asset", nil
}

type artifactStoreStub struct {
	installed   bool
	removed     bool
	quarantined bool
	quarantine  *quarantineStub
	err         error
}

type quarantineStub struct {
	restored bool
	removed  bool
}

func (value *quarantineStub) Restore() error { value.restored = true; return nil }
func (value *quarantineStub) Remove() error  { value.removed = true; return nil }

func (artifacts *artifactStoreStub) Install(context.Context, githubrelease.Release, string, pluginartifact.Downloader) (pluginartifact.Paths, error) {
	artifacts.installed = true
	return pluginartifact.Paths{}, artifacts.err
}

func (*artifactStoreStub) Verify(manifest.Manifest) (pluginartifact.Paths, error) {
	return pluginartifact.Paths{}, nil
}

func (artifacts *artifactStoreStub) RemoveRelease(string, string) error {
	artifacts.removed = true
	return nil
}

func (artifacts *artifactStoreStub) QuarantinePlugin(string) (pluginartifact.QuarantinedPlugin, error) {
	artifacts.quarantined = true
	if artifacts.quarantine == nil {
		artifacts.quarantine = &quarantineStub{}
	}
	return artifacts.quarantine, nil
}

func (*artifactStoreStub) OpenUIFile(string, string, string) (*os.File, os.FileInfo, error) {
	return nil, nil, os.ErrNotExist
}

type pluginRuntimeStub struct {
	switched   *store.PluginVersion
	rolledBack bool
	stopped    bool
	switchErr  error
}

func (runtime *pluginRuntimeStub) Switch(_ context.Context, value store.PluginVersion) (bool, error) {
	runtime.switched = &value
	return false, runtime.switchErr
}

func (runtime *pluginRuntimeStub) Rollback(context.Context, string, *store.PluginVersion) error {
	runtime.rolledBack = true
	return nil
}

func (runtime *pluginRuntimeStub) StopPlugin(context.Context, string) error {
	runtime.stopped = true
	return nil
}
func (*pluginRuntimeStub) InvokeUI(context.Context, string, string, []byte) ([]byte, error) {
	return []byte(`{}`), nil
}
func (*pluginRuntimeStub) RenderSubscription(context.Context, string, *centerpluginv1.RenderSubscriptionRequest) (*centerpluginv1.RenderSubscriptionResponse, error) {
	return nil, errors.New("subscription unavailable")
}

func TestPluginReleaseInspectionApprovalAndPrivateInstall(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	pluginManifest.Permissions = []manifest.Permission{{Name: centerpluginv1.PermissionNodesRead, Reason: "Read node state."}}
	release := managedRelease(pluginManifest)
	releases := &releaseClientStub{release: release}
	artifacts := &artifactStoreStub{}
	runtime := &pluginRuntimeStub{}
	if err := service.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	candidate, err := service.InspectPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin.git", Version: "1.2.3", GitHubToken: "private-token",
	})
	if err != nil || candidate.Manifest.ID != pluginManifest.ID || candidate.Update {
		t.Fatalf("InspectPluginRelease() = %+v, %v", candidate, err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: "1.2.3", GitHubToken: "private-token",
	}); fieldName(err) != "approved_permissions" {
		t.Fatalf("InstallPluginRelease() missing approval error = %v", err)
	}
	installed, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: "1.2.3", GitHubToken: "private-token",
		ApprovedPermissions: []string{centerpluginv1.PermissionNodesRead},
	})
	if err != nil {
		t.Fatalf("InstallPluginRelease() error = %v", err)
	}
	if installed.ActiveVersion != "1.2.3" || !artifacts.installed || runtime.switched == nil || releases.token != "private-token" {
		t.Fatalf("installed = %+v, artifacts=%v runtime=%+v token=%q", installed, artifacts.installed, runtime.switched, releases.token)
	}
	ciphertext, err := service.store.Secret(t.Context(), store.PluginInstallationSecretOwnerType, pluginManifest.ID, store.PluginInstallationGitHubToken)
	if err != nil || string(ciphertext) == "private-token" || strings.Contains(string(ciphertext), "private-token") {
		t.Fatalf("stored token ciphertext = %q, %v", ciphertext, err)
	}
}

func TestPublicPluginInspectionDoesNotRequireSecretRecovery(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	releases := &releaseClientStub{release: managedRelease(pluginManifest)}
	if err := service.ConfigurePluginLifecycle(releases, &artifactStoreStub{}, &pluginRuntimeStub{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	}); err != nil {
		t.Fatal(err)
	}
	service.secrets = nil
	candidate, err := service.InspectPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	})
	if err != nil || !candidate.Update || releases.token != "" {
		t.Fatalf("InspectPluginRelease() = %+v, %v, token = %q", candidate, err, releases.token)
	}
}

func TestReplacePluginGitHubTokenEncryptsAndAuditsWithoutTokenMaterial(t *testing.T) {
	service := newTestService(t)
	now := time.Date(2026, time.August, 2, 18, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	pluginManifest := managedRuntimeManifest()
	if err := service.store.CreatePluginInstallation(t.Context(), store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := service.ReplacePluginGitHubToken(t.Context(), pluginManifest.ID, " replacement-token "); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := service.store.Secret(
		t.Context(), store.PluginInstallationSecretOwnerType, pluginManifest.ID, store.PluginInstallationGitHubToken,
	)
	if err != nil || strings.Contains(string(ciphertext), "replacement-token") {
		t.Fatalf("stored ciphertext = %q, %v", ciphertext, err)
	}
	plaintext, err := service.secrets.Decrypt(
		store.PluginInstallationSecretOwnerType, pluginManifest.ID, store.PluginInstallationGitHubToken, ciphertext,
	)
	if err != nil || string(plaintext) != "replacement-token" {
		t.Fatalf("decrypted token = %q, %v", plaintext, err)
	}
	audit, err := service.store.ListAudit(t.Context(), 0, 10)
	if err != nil || len(audit) < 1 || audit[0].Action != "plugin.github_token.replace" {
		t.Fatalf("token audit = %+v, %v", audit, err)
	}
	encoded, err := json.Marshal(audit[0].Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if encoded := strings.ToLower(string(encoded)); strings.Contains(encoded, "replacement-token") || strings.Contains(encoded, "github_token") {
		t.Fatalf("token audit contains token material: %s", encoded)
	}
	if err := service.ReplacePluginGitHubToken(t.Context(), pluginManifest.ID, " "); fieldName(err) != "github_token" {
		t.Fatalf("empty token error = %v", err)
	}
}

func TestPluginInstallActivationFailureRemovesNewRelease(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	releases := &releaseClientStub{release: managedRelease(pluginManifest)}
	artifacts := &artifactStoreStub{}
	runtime := &pluginRuntimeStub{switchErr: errors.New("unhealthy")}
	if err := service.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: "1.2.3",
	}); fieldName(err) != "release" {
		t.Fatalf("InstallPluginRelease() activation error = %v", err)
	}
	if !artifacts.removed {
		t.Fatal("failed candidate release was not removed")
	}
	if _, err := service.store.PluginInstallationByID(t.Context(), pluginManifest.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("failed plugin installation error = %v", err)
	}
	audit, err := service.store.ListAudit(t.Context(), 0, 10)
	if err != nil || len(audit) != 1 || audit[0].Action != "plugin.install" || audit[0].Outcome != "failure" ||
		audit[0].Metadata["stage"] != "activation" {
		t.Fatalf("failed installation audit = %+v, %v", audit, err)
	}
}

func TestPluginUpgradeFailureAuditsAutomaticRollback(t *testing.T) {
	service := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, time.August, 2, 18, 0, 0, 0, time.UTC) }
	pluginManifest := managedRuntimeManifest()
	releases := &releaseClientStub{release: managedRelease(pluginManifest)}
	artifacts := &artifactStoreStub{}
	runtime := &pluginRuntimeStub{}
	if err := service.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	}); err != nil {
		t.Fatal(err)
	}

	upgradeManifest := pluginManifest
	upgradeManifest.Version = "1.2.4"
	releases.release = managedRelease(upgradeManifest)
	releases.release.ID = 11
	runtime.switchErr = errors.New("unhealthy")
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: upgradeManifest.Version,
	}); fieldName(err) != "release" {
		t.Fatalf("InstallPluginRelease() upgrade error = %v", err)
	}
	if !runtime.rolledBack {
		t.Fatal("failed upgrade did not explicitly restore the active release")
	}
	retained, err := service.PluginInstallation(t.Context(), pluginManifest.ID)
	if err != nil || retained.ActiveVersion != pluginManifest.Version {
		t.Fatalf("retained plugin = %+v, %v", retained, err)
	}
	audit, err := service.store.ListAudit(t.Context(), 0, 10)
	if err != nil || len(audit) != 3 {
		t.Fatalf("upgrade audit = %+v, %v", audit, err)
	}
	if audit[0].Action != "plugin.rollback" || audit[0].Outcome != "success" || audit[0].ActorType != "system" ||
		audit[0].Metadata["failed_version"] != "1.2.4" || audit[0].Metadata["restored_version"] != "1.2.3" {
		t.Fatalf("rollback audit = %+v", audit[0])
	}
	if audit[1].Action != "plugin.upgrade" || audit[1].Outcome != "failure" ||
		audit[1].Metadata["stage"] != "activation" || audit[1].Metadata["previous_version"] != "1.2.3" {
		t.Fatalf("failed upgrade audit = %+v", audit[1])
	}
}

func TestPluginInspectRejectsUnsupportedPermissions(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	pluginManifest.Permissions = []manifest.Permission{{Name: "core.users.read", Reason: "Unsupported."}}
	if err := service.ConfigurePluginLifecycle(
		&releaseClientStub{release: managedRelease(pluginManifest)}, &artifactStoreStub{}, &pluginRuntimeStub{},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InspectPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: "1.2.3",
	}); fieldName(err) != "repository" {
		t.Fatalf("InspectPluginRelease() unsupported permission error = %v", err)
	}
}

func TestPluginInspectAcceptsFeatureEventPermissions(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	pluginManifest.Kind = manifest.KindFeature
	pluginManifest.Requires.AgentAPI = nil
	pluginManifest.Artifacts = pluginManifest.Artifacts[:1]
	pluginManifest.Permissions = []manifest.Permission{
		{Name: centerpluginv1.PermissionEventsRead, Reason: "Consume standard events."},
		{Name: centerpluginv1.PermissionEventsWrite, Reason: "Publish structured results."},
	}
	release := githubrelease.Release{
		ID: 10, Repository: githubrelease.Repository{Owner: "Relayward", Name: "test-plugin"}, Tag: "v" + pluginManifest.Version,
		Manifest: pluginManifest, Assets: map[manifest.ArtifactRole]githubrelease.Asset{
			manifest.ArtifactCenter: {ID: 2, Name: "center", Size: pluginManifest.Artifacts[0].Size},
		},
	}
	if err := service.ConfigurePluginLifecycle(
		&releaseClientStub{release: release}, &artifactStoreStub{}, &pluginRuntimeStub{},
	); err != nil {
		t.Fatal(err)
	}
	inspected, err := service.InspectPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	})
	if err != nil || inspected.Manifest.Kind != manifest.KindFeature || len(inspected.Manifest.Permissions) != 2 {
		t.Fatalf("InspectPluginRelease() = %+v, %v", inspected, err)
	}
}

func TestPluginInspectRejectsEventConsumptionForRuntimePlugin(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	pluginManifest.Permissions = []manifest.Permission{{Name: centerpluginv1.PermissionEventsRead, Reason: "Invalid runtime event access."}}
	if err := service.ConfigurePluginLifecycle(
		&releaseClientStub{release: managedRelease(pluginManifest)}, &artifactStoreStub{}, &pluginRuntimeStub{},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InspectPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	}); fieldName(err) != "repository" {
		t.Fatalf("InspectPluginRelease() runtime event permission error = %v", err)
	}
}

func TestPluginUninstallStopsQuarantinesAndDeletesInstallation(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	artifacts := &artifactStoreStub{}
	runtime := &pluginRuntimeStub{}
	if err := service.ConfigurePluginLifecycle(
		&releaseClientStub{release: managedRelease(pluginManifest)}, artifacts, runtime,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
		GitHubToken: "private-token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UninstallPlugin(t.Context(), pluginManifest.ID); err != nil {
		t.Fatalf("UninstallPlugin() error = %v", err)
	}
	if !runtime.stopped || !artifacts.quarantined || artifacts.quarantine == nil || !artifacts.quarantine.removed {
		t.Fatalf("uninstall state: runtime=%+v artifacts=%+v", runtime, artifacts)
	}
	if _, err := service.store.PluginInstallationByID(t.Context(), pluginManifest.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("PluginInstallationByID() after uninstall error = %v", err)
	}
	if _, err := service.store.Secret(t.Context(), store.PluginInstallationSecretOwnerType, pluginManifest.ID, store.PluginInstallationGitHubToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GitHub token after uninstall error = %v", err)
	}
}

func TestPluginUninstallRestoresRuntimeWhenNodeInstanceIsStillPresent(t *testing.T) {
	service := newTestService(t)
	pluginManifest := managedRuntimeManifest()
	releases := &releaseClientStub{release: managedRelease(pluginManifest)}
	artifacts := &artifactStoreStub{}
	runtime := &pluginRuntimeStub{}
	if err := service.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
	}); err != nil {
		t.Fatal(err)
	}
	node := registerManagedAgent(t, service, "Used plugin node", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	if _, err := service.ReconcileNodePlugin(t.Context(), node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: pluginManifest.Version,
		Configuration: json.RawMessage(`{"enabled":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.UninstallPlugin(t.Context(), pluginManifest.ID); !errors.Is(err, store.ErrStateConflict) {
		t.Fatalf("UninstallPlugin() error = %v", err)
	}
	if !runtime.stopped || !runtime.rolledBack || artifacts.quarantine == nil || !artifacts.quarantine.restored {
		t.Fatalf("rollback state: runtime=%+v artifacts=%+v", runtime, artifacts)
	}
	if _, err := service.PluginInstallation(t.Context(), pluginManifest.ID); err != nil {
		t.Fatalf("plugin installation was not retained: %v", err)
	}
}

func managedRelease(value manifest.Manifest) githubrelease.Release {
	return githubrelease.Release{
		ID: 10, Repository: githubrelease.Repository{Owner: "Relayward", Name: "test-plugin"}, Tag: "v" + value.Version,
		Manifest: value, Assets: map[manifest.ArtifactRole]githubrelease.Asset{
			manifest.ArtifactCenter: {ID: 2, Name: "center", Size: value.Artifacts[0].Size},
			manifest.ArtifactNode:   {ID: 3, Name: "node", Size: value.Artifacts[1].Size},
		},
	}
}
