package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

type serverReleaseClient struct {
	release githubrelease.Release
	token   string
}

func (client *serverReleaseClient) Inspect(_ context.Context, _, _, token string) (githubrelease.Release, error) {
	client.token = token
	return client.release, nil
}

func (*serverReleaseClient) DownloadAsset(context.Context, githubrelease.Repository, githubrelease.Asset, string, string, io.Writer) error {
	return nil
}

func (*serverReleaseClient) ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error) {
	return "https://release-assets.githubusercontent.com/private-asset", nil
}

type serverArtifactStore struct {
	quarantine *serverQuarantine
	uiFile     string
}

func (*serverArtifactStore) Install(context.Context, githubrelease.Release, string, pluginartifact.Downloader) (pluginartifact.Paths, error) {
	return pluginartifact.Paths{}, nil
}

func (*serverArtifactStore) Verify(manifest.Manifest) (pluginartifact.Paths, error) {
	return pluginartifact.Paths{}, nil
}

func (*serverArtifactStore) RemoveRelease(string, string) error { return nil }

func (artifacts *serverArtifactStore) QuarantinePlugin(string) (pluginartifact.QuarantinedPlugin, error) {
	artifacts.quarantine = &serverQuarantine{}
	return artifacts.quarantine, nil
}

func (artifacts *serverArtifactStore) OpenUIFile(_, _, name string) (*os.File, os.FileInfo, error) {
	if name != "index.html" || artifacts.uiFile == "" {
		return nil, nil, os.ErrNotExist
	}
	file, err := os.Open(artifacts.uiFile)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

type serverQuarantine struct{ removed bool }

func (*serverQuarantine) Restore() error { return nil }
func (value *serverQuarantine) Remove() error {
	value.removed = true
	return nil
}

type serverPluginRuntime struct {
	active      *store.PluginVersion
	stopped     bool
	renderCalls int
	renderErr   error
}

func (runtime *serverPluginRuntime) Switch(_ context.Context, value store.PluginVersion) (bool, error) {
	runtime.active = &value
	return false, nil
}

func (runtime *serverPluginRuntime) Rollback(_ context.Context, _ string, value *store.PluginVersion) error {
	runtime.active = value
	return nil
}

func (runtime *serverPluginRuntime) StopPlugin(context.Context, string) error {
	runtime.stopped = true
	return nil
}

func (*serverPluginRuntime) InvokeUI(context.Context, string, string, []byte) ([]byte, error) {
	return []byte(`{"ok":true}`), nil
}

func (runtime *serverPluginRuntime) RenderSubscription(_ context.Context, _ string, request *centerpluginv1.RenderSubscriptionRequest) (*centerpluginv1.RenderSubscriptionResponse, error) {
	runtime.renderCalls++
	if runtime.renderErr != nil {
		return nil, runtime.renderErr
	}
	services := make([]*centerpluginv1.SubscriptionServiceContribution, len(request.Services))
	for index, service := range request.Services {
		services[index] = &centerpluginv1.SubscriptionServiceContribution{
			ServiceId: service.ServiceId, DisplayName: service.DisplayName,
			Uris:                 []string{"relayward-test://credential@edge.example.com:443#Edge"},
			MihomoProxiesJson:    [][]byte{[]byte(`{"name":"Edge","server":"edge.example.com","type":"test"}`)},
			SingBoxOutboundsJson: [][]byte{[]byte(`{"server":"edge.example.com","tag":"Edge","type":"test"}`)},
		}
	}
	return &centerpluginv1.RenderSubscriptionResponse{Services: services}, nil
}

func TestPluginLifecycleHTTPFlowDoesNotExposeSecrets(t *testing.T) {
	handler, releases, artifacts, runtime := newPluginLifecycleHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	inspectBody := []byte(`{
      "repository":"https://github.com/Relayward/test-plugin",
      "version":"1.2.3",
      "github_token":"private-token"
    }`)
	unauthenticated := performRequest(handler, http.MethodPost, "/api/v1/plugins/inspect", inspectBody, headers)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated inspect status = %d", unauthenticated.Code)
	}
	withoutCSRF := performRequest(handler, http.MethodPost, "/api/v1/plugins/inspect", inspectBody,
		map[string]string{"Content-Type": "application/json"}, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("inspect without CSRF status = %d", withoutCSRF.Code)
	}
	inspected := performRequest(handler, http.MethodPost, "/api/v1/plugins/inspect", inspectBody, headers, sessionCookie)
	if inspected.Code != http.StatusOK || strings.Contains(inspected.Body.String(), "private-token") {
		t.Fatalf("inspect status = %d, body = %s", inspected.Code, inspected.Body.String())
	}
	installBody := []byte(`{
      "repository":"https://github.com/Relayward/test-plugin",
      "version":"1.2.3",
      "github_token":"private-token",
      "approved_permissions":[]
    }`)
	installed := performRequest(handler, http.MethodPost, "/api/v1/plugins", installBody, headers, sessionCookie)
	if installed.Code != http.StatusCreated || strings.Contains(installed.Body.String(), "private-token") {
		t.Fatalf("install status = %d, body = %s", installed.Code, installed.Body.String())
	}
	var installation pluginInstallationResponse
	decodeResponse(t, installed, &installation)
	if installation.PluginID != "io.relayward.test" || installation.ActiveVersion != "1.2.3" {
		t.Fatalf("installation = %+v", installation)
	}
	listed := performRequest(handler, http.MethodGet, "/api/v1/plugins", nil, nil, sessionCookie)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), installation.PluginID) {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	detail := performRequest(handler, http.MethodGet, "/api/v1/plugins/"+installation.PluginID, nil, nil, sessionCookie)
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), "private-token") {
		t.Fatalf("detail status = %d, body = %s", detail.Code, detail.Body.String())
	}
	replacedToken := performRequest(handler, http.MethodPut, "/api/v1/plugins/"+installation.PluginID+"/github-token",
		[]byte(`{"github_token":"replacement-token"}`), headers, sessionCookie)
	if replacedToken.Code != http.StatusNoContent || strings.Contains(replacedToken.Body.String(), "replacement-token") {
		t.Fatalf("replace token status = %d, body = %s", replacedToken.Code, replacedToken.Body.String())
	}
	ui := performRequest(handler, http.MethodPost, "/api/v1/plugins/"+installation.PluginID+"/ui/status.read",
		[]byte(`{}`), headers, sessionCookie)
	if ui.Code != http.StatusOK || strings.TrimSpace(ui.Body.String()) != `{"ok":true}` {
		t.Fatalf("UI RPC status = %d, body = %s", ui.Code, ui.Body.String())
	}
	uiAssetPath := "/api/v1/plugins/" + installation.PluginID + "/ui/index.html"
	unauthenticatedAsset := performRequest(handler, http.MethodGet, uiAssetPath, nil, nil)
	if unauthenticatedAsset.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated plugin UI status = %d", unauthenticatedAsset.Code)
	}
	uiAsset := performRequest(handler, http.MethodGet, uiAssetPath, nil, nil, sessionCookie)
	if uiAsset.Code != http.StatusOK || !strings.Contains(uiAsset.Body.String(), "plugin-ui") ||
		uiAsset.Header().Get("X-Frame-Options") != "" || !strings.Contains(uiAsset.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("plugin UI asset status = %d, headers = %+v, body = %s", uiAsset.Code, uiAsset.Header(), uiAsset.Body.String())
	}
	releases.release = serverPluginRelease("1.2.4")
	upgraded := performRequest(handler, http.MethodPost, "/api/v1/plugins/"+installation.PluginID+"/upgrade",
		[]byte(`{"version":"1.2.4","approved_permissions":[]}`), headers, sessionCookie)
	if upgraded.Code != http.StatusOK {
		t.Fatalf("upgrade status = %d, body = %s", upgraded.Code, upgraded.Body.String())
	}
	decodeResponse(t, upgraded, &installation)
	if installation.ActiveVersion != "1.2.4" || installation.PreviousVersion != "1.2.3" || releases.token != "replacement-token" {
		t.Fatalf("upgraded installation = %+v, reused token = %q", installation, releases.token)
	}
	uninstalled := performRequest(handler, http.MethodDelete, "/api/v1/plugins/"+installation.PluginID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if uninstalled.Code != http.StatusNoContent || !runtime.stopped || artifacts.quarantine == nil || !artifacts.quarantine.removed {
		t.Fatalf("uninstall status = %d runtime=%+v artifacts=%+v", uninstalled.Code, runtime, artifacts)
	}
	missing := performRequest(handler, http.MethodGet, "/api/v1/plugins/"+installation.PluginID, nil, nil, sessionCookie)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("plugin after uninstall status = %d", missing.Code)
	}
}

func newPluginLifecycleHandler(t *testing.T) (http.Handler, *serverReleaseClient, *serverArtifactStore, *serverPluginRuntime) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(t.Context(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	events, err := eventstore.Open(t.Context(), filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = events.Close() })
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.NewService(database, secrets)
	if err != nil {
		t.Fatal(err)
	}
	releases := &serverReleaseClient{release: serverPluginRelease("1.2.3")}
	uiFile := filepath.Join(directory, "plugin-ui.html")
	if err := os.WriteFile(uiFile, []byte(`<main id="plugin-ui">Plugin UI</main>`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts := &serverArtifactStore{uiFile: uiFile}
	runtime := &serverPluginRuntime{}
	manager := management.NewService(database, secrets)
	if err := manager.ConfigurePluginLifecycle(releases, artifacts, runtime); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{
		Version: "test", Store: database, EventStore: events, Auth: authentication,
		Management: manager, Secrets: secrets, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return handler, releases, artifacts, runtime
}

func serverPluginRelease(version string) githubrelease.Release {
	pluginManifest := serverRuntimeManifest()
	pluginManifest.Version = version
	uiAPI := uint32(1)
	pluginManifest.Requires.UIAPI = &uiAPI
	pluginManifest.Artifacts = append(pluginManifest.Artifacts, manifest.Artifact{
		Role: manifest.ArtifactUI, File: "ui.tar.gz", Size: 128, SHA256: strings.Repeat("c", 64),
	})
	return githubrelease.Release{
		ID: 10, Repository: githubrelease.Repository{Owner: "Relayward", Name: "test-plugin"}, Tag: "v" + version,
		Manifest: pluginManifest, Assets: map[manifest.ArtifactRole]githubrelease.Asset{
			manifest.ArtifactCenter: {ID: 2, Name: "center", Size: pluginManifest.Artifacts[0].Size},
			manifest.ArtifactNode:   {ID: 3, Name: "node", Size: pluginManifest.Artifacts[1].Size},
			manifest.ArtifactUI:     {ID: 4, Name: "ui.tar.gz", Size: pluginManifest.Artifacts[2].Size},
		},
	}
}
