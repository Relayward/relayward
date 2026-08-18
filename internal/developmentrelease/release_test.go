package developmentrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
)

type upstreamStub struct {
	inspected bool
}

func (stub *upstreamStub) Inspect(context.Context, string, string, string) (githubrelease.Release, error) {
	stub.inspected = true
	return githubrelease.Release{}, nil
}
func (*upstreamStub) ListStableVersions(context.Context, string, string) ([]githubrelease.ReleaseVersion, error) {
	return nil, nil
}
func (*upstreamStub) DownloadAsset(context.Context, githubrelease.Repository, githubrelease.Asset, string, string, io.Writer) error {
	return nil
}
func (*upstreamStub) ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error) {
	return "https://example.invalid/artifact", nil
}

func TestClientLoadsAndDownloadsDevelopmentRelease(t *testing.T) {
	directory := t.TempDir()
	center := []byte("center")
	node := []byte("node")
	writeRelease(t, directory, "1.2.3-dev.1", center, node)
	upstream := &upstreamStub{}
	client, err := Open(directory, "https://github.com/Relayward/plugin", upstream)
	if err != nil {
		t.Fatal(err)
	}
	release, err := client.Inspect(t.Context(), "https://github.com/Relayward/plugin", "1.2.3-dev.1", "")
	if err != nil || release.Manifest.Version != "1.2.3-dev.1" || upstream.inspected {
		t.Fatalf("Inspect() = %+v, %v, upstream = %t", release, err, upstream.inspected)
	}
	asset := release.Assets[manifest.ArtifactNode]
	var output bytes.Buffer
	if err := client.DownloadAsset(t.Context(), release.Repository, asset, "", release.Manifest.Artifacts[1].SHA256, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), node) {
		t.Fatalf("downloaded node = %q", output.Bytes())
	}
	if _, err := client.Inspect(t.Context(), release.Repository.URL(), release.Manifest.Version, "token"); err == nil {
		t.Fatal("development release accepted a token")
	}
}

func TestClientRejectsChangedAndUnsafeArtifacts(t *testing.T) {
	directory := t.TempDir()
	writeRelease(t, directory, "1.2.3-dev.1", []byte("center"), []byte("node"))
	client, err := Open(directory, "https://github.com/Relayward/plugin", &upstreamStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "node"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := client.Release()
	var output bytes.Buffer
	if err := client.DownloadAsset(t.Context(), release.Repository, release.Assets[manifest.ArtifactNode], "", strings.Repeat("0", 64), &output); err == nil {
		t.Fatal("changed artifact passed verification")
	}
}

func writeRelease(t *testing.T, directory, version string, center, node []byte) {
	t.Helper()
	for name, raw := range map[string][]byte{"center": center, "node": node} {
		if err := os.WriteFile(filepath.Join(directory, name), raw, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agentAPI := uint32(1)
	value := manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.test", Name: "Test", Version: version,
		Kind: manifest.KindRuntime, Requires: manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI},
		Artifacts: []manifest.Artifact{
			releaseArtifact(manifest.ArtifactCenter, "center", center),
			releaseArtifact(manifest.ArtifactNode, "node", node),
		},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, githubrelease.ManifestAssetName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func releaseArtifact(role manifest.ArtifactRole, name string, raw []byte) manifest.Artifact {
	digest := sha256.Sum256(raw)
	return manifest.Artifact{
		Role: role, File: name, Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), OS: "linux", Arch: "amd64",
	}
}
