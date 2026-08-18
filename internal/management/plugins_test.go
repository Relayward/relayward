package management

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

func TestNodePluginReconcileEncryptedDeliveryAndCompletion(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 2, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	node := registerManagedAgent(t, service, "Plugin node", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	pluginManifest := managedRuntimeManifest()
	if err := service.store.CreatePluginInstallation(ctx, store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin.git",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
	configuration := json.RawMessage(`{"credential":"must-not-leak","listen":1080}`)
	created, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: pluginManifest.Version, Configuration: configuration,
	})
	if err != nil {
		t.Fatalf("ReconcileNodePlugin() error = %v", err)
	}
	if created.Generation != 1 || created.CommandStatus != store.AgentCommandPending || created.DesiredConfigurationSHA256 == "" {
		t.Fatalf("created node plugin = %+v", created)
	}
	command, err := service.NextAgentCommand(ctx, node.ID, now.Add(time.Second))
	if err != nil || !command.RequestEncrypted {
		t.Fatalf("NextAgentCommand() = %+v, %v", command, err)
	}
	reconcile, err := agentv1.DecodePluginReconcileCommand(command.Request)
	if err != nil {
		t.Fatalf("DecodePluginReconcileCommand() error = %v", err)
	}
	if reconcile.PluginID != pluginManifest.ID || reconcile.Generation != 1 || string(reconcile.Configuration) != string(configuration) ||
		reconcile.Artifact == nil || reconcile.Artifact.DownloadURL != "https://github.com/Relayward/test-plugin/releases/download/v1.2.3/node" {
		t.Fatalf("decrypted plugin command = %+v", reconcile)
	}
	output, err := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: reconcile.PluginID, Generation: reconcile.Generation, State: reconcile.DesiredState,
		Version: reconcile.Version, ConfigurationSHA256: created.DesiredConfigurationSHA256,
	})
	if err != nil {
		t.Fatalf("EncodePluginReconcileOutput() error = %v", err)
	}
	result := agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: now.Add(2 * time.Minute), Output: output,
	}
	if err := service.CompleteAgentCommand(ctx, node.ID, node.CredentialHash, result, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() error = %v", err)
	}
	if err := service.CompleteAgentCommand(ctx, node.ID, node.CredentialHash, result, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() replay error = %v", err)
	}
	actual, err := service.NodePluginInstance(ctx, node.ID, pluginManifest.ID)
	if err != nil || actual.ActualState != agentv1.PluginStateRunning || actual.ReconcileStatus != store.AgentCommandSucceeded {
		t.Fatalf("NodePluginInstance() = %+v, %v", actual, err)
	}
	items, err := service.ListNodePluginInstances(ctx)
	if err != nil || len(items) != 1 || items[0].PluginName != pluginManifest.Name {
		t.Fatalf("ListNodePluginInstances() = %+v, %v", items, err)
	}
	if _, err := service.store.Secret(ctx, store.AgentCommandSecretOwnerType, command.ID, store.AgentCommandRequestSecret); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("completed command ciphertext error = %v", err)
	}
	now = now.Add(4 * time.Minute)
	reused, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateStopped, Version: pluginManifest.Version,
	})
	if err != nil || reused.Generation != 2 {
		t.Fatalf("ReconcileNodePlugin() reused configuration = %+v, %v", reused, err)
	}
	reusedCommand, err := service.NextAgentCommand(ctx, node.ID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NextAgentCommand() reused configuration error = %v", err)
	}
	reusedRequest, err := agentv1.DecodePluginReconcileCommand(reusedCommand.Request)
	if err != nil || string(reusedRequest.Configuration) != string(configuration) || reusedRequest.DesiredState != agentv1.PluginStateStopped {
		t.Fatalf("reused plugin configuration = %+v, %v", reusedRequest, err)
	}
}

func TestNodePluginReconcileValidationAndSecretsGate(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 2, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	withoutSupervision := registerManagedAgent(t, service, "Legacy node", []string{agentv1.CapabilityControlCommands})
	pluginManifest := managedRuntimeManifest()
	if err := service.store.CreatePluginInstallation(ctx, store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
	input := NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: pluginManifest.Version,
		Configuration: json.RawMessage(`{"enabled":true}`),
	}
	if _, err := service.ReconcileNodePlugin(ctx, withoutSupervision.ID, pluginManifest.ID, input); fieldName(err) != "node_id" {
		t.Fatalf("ReconcileNodePlugin() capability error = %v", err)
	}
	node := registerManagedAgent(t, service, "Plugin node", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	wrongVersion := input
	wrongVersion.Version = "2.0.0"
	if _, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, wrongVersion); fieldName(err) != "version" {
		t.Fatalf("ReconcileNodePlugin() version error = %v", err)
	}
	if _, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateAbsent, Version: pluginManifest.Version,
	}); fieldName(err) != "body" {
		t.Fatalf("ReconcileNodePlugin() non-empty absent error = %v", err)
	}
	availableSecrets := service.secrets
	service.secrets = nil
	if _, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, input); !errors.Is(err, secretbox.ErrUnavailable) {
		t.Fatalf("ReconcileNodePlugin() unavailable secrets error = %v", err)
	}
	service.secrets = availableSecrets
}

func TestPluginOwnedNodeConfigurationUsesGenerationAndPluginAudit(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	node := registerManagedAgent(t, service, "Plugin-managed node", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	pluginManifest := managedRuntimeManifest()
	if err := service.store.CreatePluginInstallation(ctx, store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatal(err)
	}
	configuration := json.RawMessage(`{"xray_version":"26.3.27","xray_config":{}}`)
	created, err := service.ConfigureNodePlugin(ctx, node.ID, pluginManifest.ID, pluginManifest.Version, 0, configuration)
	if err != nil || created.Generation != 1 || created.DesiredState != agentv1.PluginStateRunning {
		t.Fatalf("ConfigureNodePlugin() = %+v, %v", created, err)
	}
	instance, stored, err := service.NodePluginConfiguration(ctx, node.ID, pluginManifest.ID)
	if err != nil || instance.Generation != 1 || string(stored) != string(configuration) {
		t.Fatalf("NodePluginConfiguration() = %+v, %s, %v", instance, stored, err)
	}
	if _, err := service.ConfigureNodePlugin(ctx, node.ID, pluginManifest.ID, pluginManifest.Version, 0, configuration); !errors.Is(err, store.ErrGenerationConflict) {
		t.Fatalf("ConfigureNodePlugin() stale generation error = %v", err)
	}
	unchanged, err := service.NodePluginInstance(ctx, node.ID, pluginManifest.ID)
	if err != nil || unchanged.Generation != 1 {
		t.Fatalf("node plugin after stale write = %+v, %v", unchanged, err)
	}
	audit, err := service.store.ListAudit(ctx, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if entry.Action == "node.plugin_reconcile.request" {
			found = true
			if entry.ActorType != "plugin" || entry.ActorID != pluginManifest.ID {
				t.Fatalf("plugin reconciliation audit = %+v", entry)
			}
		}
	}
	if !found {
		t.Fatal("plugin reconciliation audit was not recorded")
	}
}

func TestNodePluginReconcileResolvesPrivateReleaseAsset(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 2, 15, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	node := registerManagedAgent(t, service, "Private plugin node", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	pluginManifest := managedRuntimeManifest()
	release := managedRelease(pluginManifest)
	releases := &releaseClientStub{
		release: release, resolvedURL: "https://release-assets.githubusercontent.com/private-node-asset",
	}
	if err := service.ConfigurePluginLifecycle(releases, &artifactStoreStub{}, &pluginRuntimeStub{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.InstallPluginRelease(ctx, PluginReleaseInput{
		Repository: "https://github.com/Relayward/test-plugin", Version: pluginManifest.Version,
		GitHubToken: "private-token",
	}); err != nil {
		t.Fatalf("InstallPluginRelease() error = %v", err)
	}
	if _, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: pluginManifest.Version,
		Configuration: json.RawMessage(`{"enabled":true}`),
	}); err != nil {
		t.Fatalf("ReconcileNodePlugin() error = %v", err)
	}
	command, err := service.NextAgentCommand(ctx, node.ID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NextAgentCommand() error = %v", err)
	}
	reconcile, err := agentv1.DecodePluginReconcileCommand(command.Request)
	if err != nil {
		t.Fatalf("DecodePluginReconcileCommand() error = %v", err)
	}
	if reconcile.Artifact == nil || reconcile.Artifact.DownloadURL != releases.resolvedURL {
		t.Fatalf("private plugin command artifact = %+v", reconcile.Artifact)
	}
	if releases.resolvedRepository != release.Repository || releases.resolvedAssetID != 3 || releases.resolvedToken != "private-token" {
		t.Fatalf("ResolveAssetURL() = repository %+v, asset %d, token %q", releases.resolvedRepository, releases.resolvedAssetID, releases.resolvedToken)
	}
}

func TestPublicReleaseAssetURL(t *testing.T) {
	value, err := publicReleaseAssetURL("https://github.com/Relayward/plugin.git", "1.2.3", "node linux")
	if err != nil || value != "https://github.com/Relayward/plugin/releases/download/v1.2.3/node%20linux" {
		t.Fatalf("publicReleaseAssetURL() = %q, %v", value, err)
	}
	for _, invalid := range []string{
		"http://github.com/Relayward/plugin", "https://example.com/Relayward/plugin",
		"https://github.com/Relayward/plugin/extra", "https://token@github.com/Relayward/plugin",
	} {
		if _, err := publicReleaseAssetURL(invalid, "1.2.3", "node"); err == nil {
			t.Fatalf("publicReleaseAssetURL() accepted %q", invalid)
		}
	}
}

func TestDevelopmentPluginUsesCenterArtifactURL(t *testing.T) {
	service := newTestService(t)
	release := managedRelease(managedRuntimeManifest())
	releases := &releaseClientStub{release: release}
	if err := service.ConfigurePluginLifecycle(releases, &artifactStoreStub{}, &pluginRuntimeStub{}); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureDevelopmentPluginRelease(release, "https://development.example.com"); err != nil {
		t.Fatal(err)
	}
	installation := store.PluginInstallation{
		PluginID: release.Manifest.ID, Repository: release.Repository.URL(), ActiveVersion: release.Manifest.Version,
	}
	artifact, _ := nodeArtifact(release.Manifest)
	value, err := service.nodePluginArtifactURL(t.Context(), installation, artifact)
	if err != nil || value != "https://development.example.com/development-artifacts/io.relayward.test/1.2.3/node" {
		t.Fatalf("nodePluginArtifactURL() = %q, %v", value, err)
	}
	installation.ActiveVersion = "1.2.2"
	value, err = service.nodePluginArtifactURL(t.Context(), installation, artifact)
	if err != nil || value != "https://github.com/Relayward/test-plugin/releases/download/v1.2.2/node" {
		t.Fatalf("stable nodePluginArtifactURL() = %q, %v", value, err)
	}
}

func managedRuntimeManifest() manifest.Manifest {
	agentAPI := uint32(1)
	return manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.test", Name: "Test Runtime",
		Version: "1.2.3", Kind: manifest.KindRuntime,
		Requires:    manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI},
		Permissions: []manifest.Permission{},
		Artifacts: []manifest.Artifact{
			{Role: manifest.ArtifactCenter, File: "center", Size: 1234, SHA256: strings.Repeat("a", 64), OS: "linux", Arch: "amd64"},
			{Role: manifest.ArtifactNode, File: "node", Size: 1234, SHA256: strings.Repeat("b", 64), OS: "linux", Arch: "amd64"},
		},
	}
}
