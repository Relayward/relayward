package management

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"

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
	runtime, err := pluginruntime.New(service.store, artifacts, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := service.store.CreateNode(t.Context(), store.Node{ID: "node-1", Name: "Edge", Enabled: true}, time.Now().UTC()); err != nil {
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
		ApprovedPermissions: []string{centerpluginv1.PermissionNodesRead},
	})
	if err != nil {
		t.Fatalf("InstallPluginRelease() error = %v", err)
	}
	if installed.Health != "healthy" || installed.ActiveVersion != "1.2.3" {
		t.Fatalf("installed plugin = %+v", installed)
	}
	raw, err := service.InvokePluginUI(t.Context(), installed.PluginID, "nodes.summary", []byte(`{}`))
	if err != nil || string(raw) != `{"count":1}` {
		t.Fatalf("InvokePluginUI() = %s, %v", raw, err)
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

	releases.release, releases.assets = integrationRelease("1.2.4", executable, ui)
	if _, err := service.InstallPluginRelease(t.Context(), PluginReleaseInput{
		Repository: releases.release.Repository.URL(), Version: "1.2.4",
		ApprovedPermissions: []string{centerpluginv1.PermissionNodesRead},
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
	if err := service.UninstallPlugin(t.Context(), installed.PluginID); err != nil {
		t.Fatalf("UninstallPlugin() error = %v", err)
	}
	if _, err := service.PluginInstallation(t.Context(), installed.PluginID); err == nil {
		t.Fatal("plugin installation remains after uninstall")
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
		Requires:    manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI, UIAPI: &uiAPI},
		Permissions: []manifest.Permission{{Name: centerpluginv1.PermissionNodesRead, Reason: "Exercise node access."}},
		Artifacts:   []manifest.Artifact{centerArtifact, nodeArtifact, uiArtifact},
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
