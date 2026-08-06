package management

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"
	nodepluginv1 "github.com/Relayward/relayward-sdk/nodeplugin/v1"

	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/pluginruntime"
	"github.com/Relayward/relayward/internal/store"
)

type integrationReleaseClient struct {
	release githubrelease.Release
	assets  map[int64][]byte
}

func (client *integrationReleaseClient) Inspect(context.Context, string, string, string) (githubrelease.Release, error) {
	return client.release, nil
}

func (client *integrationReleaseClient) DownloadAsset(_ context.Context, _ githubrelease.Repository, asset githubrelease.Asset,
	_ string, _ string, destination io.Writer,
) error {
	_, err := destination.Write(client.assets[asset.ID])
	return err
}

func (*integrationReleaseClient) ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error) {
	return "https://release-assets.githubusercontent.com/contract-node", nil
}

func TestPluginLifecycleRunsRealReleaseAndRestoresAfterBadUpgrade(t *testing.T) {
	service := newTestService(t)
	root, err := os.MkdirTemp("", "rwm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	artifacts, err := pluginartifact.Open(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	events, err := eventstore.Open(t.Context(), filepath.Join(root, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	runtime, err := pluginruntime.New(service.store, artifacts, events, service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	if err := runtime.Start(runtimeContext); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancelRuntime()
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Close(closeContext)
	})
	node := registerManagedAgent(t, service, "Edge", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	node, err = service.UpdateNode(t.Context(), node.ID, NodeInput{
		Name: node.Name, PublicAddress: "edge.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile(mustExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	ui := integrationUIArchive(t)
	releases := &integrationReleaseClient{}
	releases.release, releases.assets = integrationRelease("1.2.3", executable, ui)
	if err := service.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	installed, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: releases.release.Repository.URL(), Version: "1.2.3", GitHubToken: "private-token",
		ApprovedPermissions: []string{centerpluginv1.PermissionEventsWrite, centerpluginv1.PermissionNodesRead, centerpluginv1.PermissionServicesWrite},
	})
	if err != nil {
		t.Fatalf("InstallPluginRelease() error = %v", err)
	}
	if installed.Health != "healthy" || installed.ActiveVersion != "1.2.3" {
		t.Fatalf("installed plugin = %+v", installed)
	}
	instance, err := service.ReconcileNodePlugin(t.Context(), node.ID, installed.PluginID, NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: installed.ActiveVersion,
		Configuration: []byte(`{"enabled":true}`),
	})
	if err != nil {
		t.Fatalf("ReconcileNodePlugin() error = %v", err)
	}
	completeIntegrationPluginCommand(t, service, node)
	if err := service.RecordNodePluginStatus(t.Context(), node.ID, agentv1.PluginStatusEvent{
		PluginID: installed.PluginID, Generation: instance.Generation, State: agentv1.PluginStateRunning,
		Version: installed.ActiveVersion, ConfigurationSHA256: instance.DesiredConfigurationSHA256,
		Health: agentv1.PluginHealthHealthy,
		Capabilities: []string{
			nodepluginv1.CapabilityRecentActivity, nodepluginv1.CapabilityDynamicBlocking,
			nodepluginv1.CapabilityServiceControl, nodepluginv1.CapabilityTrafficCounters,
		},
	}, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("RecordNodePluginStatus() error = %v", err)
	}
	raw, err := service.InvokePluginUI(t.Context(), installed.PluginID, "nodes.summary", []byte(`{}`))
	if err != nil || string(raw) != `{"count":1}` {
		t.Fatalf("InvokePluginUI() = %s, %v", raw, err)
	}
	eventInput := []byte(fmt.Sprintf(`{"node_id":%q,"source_event_id":"contract-event-1","observed_at_unix_nano":%d}`, node.ID, time.Now().UTC().UnixNano()))
	raw, err = service.InvokePluginUI(t.Context(), installed.PluginID, "events.publish", eventInput)
	if err != nil || string(raw) != `{"event_count":1}` {
		t.Fatalf("InvokePluginUI(events.publish) = %s, %v", raw, err)
	}
	if count, err := events.Count(t.Context()); err != nil || count != 1 {
		t.Fatalf("published event count = %d, %v", count, err)
	}
	file, _, err := service.OpenPluginUIFile(t.Context(), installed.PluginID, "index.html")
	if err != nil {
		t.Fatalf("OpenPluginUIFile() error = %v", err)
	}
	page, readErr := io.ReadAll(file)
	_ = file.Close()
	if readErr != nil || !strings.Contains(string(page), "contract-ui") {
		t.Fatalf("plugin UI entry = %q, %v", page, readErr)
	}
	raw, err = service.InvokePluginUI(t.Context(), installed.PluginID, "services.replace", []byte(`{"node_id":"`+node.ID+`"}`))
	if err != nil || string(raw) != `{"service_count":1}` {
		t.Fatalf("InvokePluginUI(services.replace) = %s, %v", raw, err)
	}
	services, err := service.ListPluginServices(t.Context(), node.ID)
	if err != nil || len(services) != 1 || services[0].ServiceID != "contract-main" {
		t.Fatalf("registered services = %+v, %v", services, err)
	}
	user, err := service.CreateUser(t.Context(), UserInput{DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.CreateAuthorization(t.Context(), DefaultAuthorizationInput(user.ID, node.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateServiceBinding(t.Context(), ServiceBindingInput{
		AuthorizationID: authorization.Authorization.ID, PluginID: installed.PluginID,
		ServiceID: "contract-main", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rendered, err := service.RenderSubscription(t.Context(), authorization.SubscriptionToken, store.SubscriptionFormatBase64)
	if err != nil {
		t.Fatalf("RenderSubscription() error = %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(rendered.Content)))
	if err != nil || !strings.Contains(string(decoded), "relayward-test://") || !strings.Contains(string(decoded), node.PublicAddress) {
		t.Fatalf("rendered subscription = %q, decode error = %v", decoded, err)
	}

	releases.release, releases.assets = integrationRelease("1.2.4", executable, ui)
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: releases.release.Repository.URL(), Version: "1.2.4",
		ApprovedPermissions: []string{centerpluginv1.PermissionEventsWrite, centerpluginv1.PermissionNodesRead, centerpluginv1.PermissionServicesWrite},
	}); fieldName(err) != "release" {
		t.Fatalf("bad upgrade error = %v", err)
	}
	retained, err := service.PluginInstallation(t.Context(), installed.PluginID)
	if err != nil || retained.ActiveVersion != "1.2.3" {
		t.Fatalf("retained installation = %+v, %v", retained, err)
	}
	raw, err = service.InvokePluginUI(t.Context(), installed.PluginID, "nodes.summary", []byte(`{}`))
	if err != nil || string(raw) != `{"count":1}` {
		t.Fatalf("InvokePluginUI() after rollback = %s, %v", raw, err)
	}
	removed, err := service.ReconcileNodePlugin(t.Context(), node.ID, installed.PluginID, NodePluginInput{
		DesiredState: agentv1.PluginStateAbsent,
	})
	if err != nil {
		t.Fatalf("remove node plugin request error = %v", err)
	}
	completeIntegrationPluginCommand(t, service, node)
	removedAt := time.Now().UTC()
	if err := service.RecordNodePluginStatus(t.Context(), node.ID, agentv1.PluginStatusEvent{
		PluginID: installed.PluginID, Generation: removed.Generation,
		State: agentv1.PluginStateAbsent, Health: agentv1.PluginHealthUnknown,
	}, removedAt, removedAt); err != nil {
		t.Fatalf("record removed node plugin error = %v", err)
	}
	if err := service.UninstallPlugin(t.Context(), installed.PluginID); err != nil {
		t.Fatalf("UninstallPlugin() error = %v", err)
	}
	if _, err := service.PluginInstallation(t.Context(), installed.PluginID); err == nil {
		t.Fatal("plugin installation remains after uninstall")
	}
}

func completeIntegrationPluginCommand(t *testing.T, service *Service, node store.Node) {
	t.Helper()
	now := time.Now().UTC()
	command, err := service.NextAgentCommand(t.Context(), node.ID, now)
	if err != nil {
		t.Fatalf("read plugin reconcile command error = %v", err)
	}
	reconcile, err := agentv1.DecodePluginReconcileCommand(command.Request)
	if err != nil {
		t.Fatalf("decode plugin reconcile command error = %v", err)
	}
	configurationSHA256 := ""
	if reconcile.DesiredState != agentv1.PluginStateAbsent {
		configurationSHA256, err = agentv1.PluginConfigurationDigest(reconcile.Configuration)
		if err != nil {
			t.Fatalf("digest plugin reconcile configuration error = %v", err)
		}
	}
	output, err := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: reconcile.PluginID, Generation: reconcile.Generation, State: reconcile.DesiredState,
		Version: reconcile.Version, ConfigurationSHA256: configurationSHA256,
	})
	if err != nil {
		t.Fatalf("encode plugin reconcile output error = %v", err)
	}
	if err := service.CompleteAgentCommand(t.Context(), node.ID, node.CredentialHash, agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256,
		Status: agentv1.CommandStatusSucceeded, CompletedAt: now, Output: output,
	}, now); err != nil {
		t.Fatalf("complete plugin reconcile command error = %v", err)
	}
}

func integrationRelease(version string, executable, ui []byte) (githubrelease.Release, map[int64][]byte) {
	agentAPI, uiAPI := uint32(1), uint32(1)
	centerArtifact := integrationArtifact(manifest.ArtifactCenter, "center", executable, "linux", "amd64")
	nodeArtifact := integrationArtifact(manifest.ArtifactNode, "node", executable, "linux", "amd64")
	uiArtifact := integrationArtifact(manifest.ArtifactUI, "ui.tar.gz", ui, "", "")
	value := manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.contract-test", Name: "Contract plugin",
		Version: version, Kind: manifest.KindRuntime,
		Requires: manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI, UIAPI: &uiAPI},
		Permissions: []manifest.Permission{
			{Name: centerpluginv1.PermissionEventsWrite, Reason: "Exercise event publication."},
			{Name: centerpluginv1.PermissionNodesRead, Reason: "Exercise node access."},
			{Name: centerpluginv1.PermissionServicesWrite, Reason: "Exercise service registration."},
		},
		Artifacts: []manifest.Artifact{centerArtifact, nodeArtifact, uiArtifact},
	}
	return githubrelease.Release{
		ID: 10, Repository: githubrelease.Repository{Owner: "Relayward", Name: "contract-plugin"}, Tag: "v" + version,
		Manifest: value, Assets: map[manifest.ArtifactRole]githubrelease.Asset{
			manifest.ArtifactCenter: {ID: 2, Name: centerArtifact.File, Size: centerArtifact.Size},
			manifest.ArtifactNode:   {ID: 3, Name: nodeArtifact.File, Size: nodeArtifact.Size},
			manifest.ArtifactUI:     {ID: 4, Name: uiArtifact.File, Size: uiArtifact.Size},
		},
	}, map[int64][]byte{2: executable, 3: executable, 4: ui}
}

func integrationArtifact(role manifest.ArtifactRole, name string, raw []byte, osName, arch string) manifest.Artifact {
	digest := sha256.Sum256(raw)
	return manifest.Artifact{
		Role: role, File: name, Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), OS: osName, Arch: arch,
	}
}

func integrationUIArchive(t *testing.T) []byte {
	t.Helper()
	var result bytes.Buffer
	compressed := gzip.NewWriter(&result)
	archive := tar.NewWriter(compressed)
	raw := []byte(`<main id="contract-ui">Contract UI</main>`)
	if err := archive.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o600, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
