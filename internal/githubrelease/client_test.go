package githubrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Relayward/relayward-sdk/manifest"
)

func TestClientInspectsAndDownloadsReleaseWithoutLeakingToken(t *testing.T) {
	center := []byte("center artifact")
	node := []byte("node artifact")
	ui := []byte("ui artifact")
	pluginManifest := releaseManifest(center, node, ui)
	manifestJSON, _ := json.Marshal(pluginManifest)
	assets := map[int64][]byte{1: manifestJSON, 2: center, 3: node, 4: ui}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/repos/Owner/plugin/releases/tags/v1.2.3":
			if request.Header.Get("Authorization") != "Bearer private-token" {
				t.Error("release request is missing its token")
			}
			writeRelease(t, writer, pluginManifest, assets)
		case strings.HasPrefix(request.URL.Path, "/repos/Owner/plugin/releases/assets/"):
			if request.Header.Get("Authorization") != "Bearer private-token" {
				t.Error("asset API request is missing its token")
			}
			id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
			http.Redirect(writer, request, server.URL+"/download/"+id, http.StatusFound)
		case strings.HasPrefix(request.URL.Path, "/download/"):
			if request.Header.Get("Authorization") != "" {
				t.Error("token leaked to release asset redirect")
			}
			var id int64
			_, _ = fmt.Sscan(strings.TrimPrefix(request.URL.Path, "/download/"), &id)
			writer.Header().Set("Content-Length", fmt.Sprint(len(assets[id])))
			_, _ = writer.Write(assets[id])
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := newClient(server.Client(), base, func(candidate *url.URL) error {
		if candidate.Host != base.Host {
			return fmt.Errorf("unexpected host")
		}
		return nil
	})

	release, err := client.Inspect(context.Background(), "https://github.com/Owner/plugin.git", "1.2.3", "private-token")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if release.Manifest.ID != pluginManifest.ID || release.Repository.URL() != "https://github.com/Owner/plugin" || len(release.Assets) != 3 {
		t.Fatalf("release = %+v", release)
	}
	var downloaded bytes.Buffer
	centerAsset := release.Assets[manifest.ArtifactCenter]
	if err := client.DownloadAsset(context.Background(), release.Repository, centerAsset, "private-token", pluginManifest.Artifacts[0].SHA256, &downloaded); err != nil {
		t.Fatalf("DownloadAsset() error = %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), center) {
		t.Fatalf("downloaded center = %q", downloaded.Bytes())
	}
	resolved, err := client.ResolveAssetURL(context.Background(), release.Repository, release.Assets[manifest.ArtifactNode].ID, "private-token")
	if err != nil || !strings.HasPrefix(resolved, server.URL+"/download/") {
		t.Fatalf("ResolveAssetURL() = %q, %v", resolved, err)
	}
}

func TestClientRejectsManifestMismatchAndUnsafeRepositories(t *testing.T) {
	for _, raw := range []string{
		"http://github.com/owner/repo", "https://token@github.com/owner/repo",
		"https://github.com/owner/repo/extra", "https://github.com.evil.invalid/owner/repo",
	} {
		if _, err := ParseRepository(raw); err == nil {
			t.Fatalf("ParseRepository(%q) succeeded", raw)
		}
	}

	pluginManifest := releaseManifest([]byte("center"), []byte("node"), []byte("ui"))
	pluginManifest.Version = "1.2.4"
	manifestJSON, _ := json.Marshal(pluginManifest)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/releases/tags/") {
			writeRelease(t, writer, pluginManifest, map[int64][]byte{1: manifestJSON, 2: []byte("center"), 3: []byte("node"), 4: []byte("ui")})
			return
		}
		if strings.HasSuffix(request.URL.Path, "/1") {
			_, _ = writer.Write(manifestJSON)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := newClient(server.Client(), base, func(*url.URL) error { return nil })
	if _, err := client.Inspect(context.Background(), "https://github.com/owner/repo", "1.2.3", ""); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("Inspect() mismatch error = %v", err)
	}
}

func TestResolveAssetURLRejectsInvalidTokenBeforeRequest(t *testing.T) {
	client := NewClient(nil)
	_, err := client.ResolveAssetURL(context.Background(), Repository{Owner: "Relayward", Name: "plugin"}, 1, "bad\ntoken")
	if err == nil || !strings.Contains(err.Error(), "token is invalid") {
		t.Fatalf("ResolveAssetURL() error = %v", err)
	}
}

func TestClientUsesLongerTimeoutForArtifactDownloads(t *testing.T) {
	client := NewClient(nil)
	if client.httpClient.Timeout != 0 {
		t.Fatalf("HTTP client timeout = %v, want operation-specific context deadlines", client.httpClient.Timeout)
	}
	if artifactDownloadTimeout <= defaultRequestTimeout {
		t.Fatalf("artifact timeout = %v, request timeout = %v", artifactDownloadTimeout, defaultRequestTimeout)
	}
}

func writeRelease(t *testing.T, writer http.ResponseWriter, value manifest.Manifest, assets map[int64][]byte) {
	t.Helper()
	response := map[string]any{
		"id": 10, "tag_name": "v1.2.3", "draft": false, "prerelease": false,
		"assets": []map[string]any{
			{"id": 1, "name": ManifestAssetName, "size": len(assets[1])},
			{"id": 2, "name": value.Artifacts[0].File, "size": len(assets[2])},
			{"id": 3, "name": value.Artifacts[1].File, "size": len(assets[3])},
			{"id": 4, "name": value.Artifacts[2].File, "size": len(assets[4])},
		},
	}
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		t.Fatal(err)
	}
}

func releaseManifest(center, node, ui []byte) manifest.Manifest {
	agentAPI, uiAPI := uint32(1), uint32(1)
	return manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.test", Name: "Test plugin",
		Version: "1.2.3", Kind: manifest.KindRuntime,
		Requires:    manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI, UIAPI: &uiAPI},
		Permissions: []manifest.Permission{{Name: "core.nodes.read", Reason: "Read node state."}},
		Artifacts: []manifest.Artifact{
			artifact(manifest.ArtifactCenter, "center", center, "linux", "amd64"),
			artifact(manifest.ArtifactNode, "node", node, "linux", "amd64"),
			artifact(manifest.ArtifactUI, "ui.tar.gz", ui, "", ""),
		},
	}
}

func artifact(role manifest.ArtifactRole, name string, raw []byte, os, arch string) manifest.Artifact {
	digest := sha256.Sum256(raw)
	return manifest.Artifact{Role: role, File: name, Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), OS: os, Arch: arch}
}
