package pluginartifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
)

type memoryDownloader struct {
	assets map[int64][]byte
	calls  []int64
}

func (downloader *memoryDownloader) DownloadAsset(_ context.Context, _ githubrelease.Repository, asset githubrelease.Asset,
	_ string, digest string, destination io.Writer,
) error {
	downloader.calls = append(downloader.calls, asset.ID)
	raw := downloader.assets[asset.ID]
	actual := sha256.Sum256(raw)
	if int64(len(raw)) != asset.Size || hex.EncodeToString(actual[:]) != digest {
		return io.ErrUnexpectedEOF
	}
	_, err := destination.Write(raw)
	return err
}

func TestStoreInstallsCenterAndSafeUIWithoutDownloadingNodeArtifact(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join(t.TempDir(), "plugins"))
	store, err := Open(root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	center := []byte("center executable")
	node := []byte("node executable")
	ui := uiArchive(t, map[string][]byte{"index.html": []byte("<main>plugin</main>"), "assets/app.js": []byte("ok")}, "")
	release := artifactRelease(center, node, ui)
	downloader := &memoryDownloader{assets: map[int64][]byte{2: center, 3: node, 4: ui}}
	paths, err := store.Install(context.Background(), release, "token", downloader)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(downloader.calls) != 2 || downloader.calls[0] != 2 || downloader.calls[1] != 4 {
		t.Fatalf("downloaded assets = %v", downloader.calls)
	}
	for path, mode := range map[string]os.FileMode{paths.Root: 0o700, paths.Executable: 0o700, filepath.Join(paths.UI, "index.html"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %v, error = %v", path, info.Mode().Perm(), err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(paths.UI, "index.html")); err != nil || string(raw) != "<main>plugin</main>" {
		t.Fatalf("UI index = %q, %v", raw, err)
	}
	if _, err := store.Verify(release.Manifest); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	file, info, err := store.OpenUIFile(release.Manifest.ID, release.Manifest.Version, "assets/app.js")
	if err != nil {
		t.Fatalf("OpenUIFile() error = %v", err)
	}
	raw, readErr := io.ReadAll(file)
	_ = file.Close()
	if readErr != nil || string(raw) != "ok" || !info.Mode().IsRegular() {
		t.Fatalf("OpenUIFile() = %q, %+v, %v", raw, info, readErr)
	}
	for _, name := range []string{"../manifest.json", "/index.html", "assets/../index.html", `assets\app.js`} {
		if _, _, err := store.OpenUIFile(release.Manifest.ID, release.Manifest.Version, name); !errors.Is(err, ErrInvalidUIPath) {
			t.Fatalf("OpenUIFile(%q) error = %v", name, err)
		}
	}
	if _, err := store.Install(context.Background(), release, "token", downloader); !errors.Is(err, ErrReleaseExists) {
		t.Fatalf("second Install() error = %v", err)
	}
}

func TestStoreRejectsUnsafeUIEntriesAndLeavesNoRelease(t *testing.T) {
	for _, unsafeName := range []string{"../escape", "/absolute", "link"} {
		t.Run(unsafeName, func(t *testing.T) {
			root, _ := filepath.Abs(filepath.Join(t.TempDir(), "plugins"))
			store, _ := Open(root)
			entryType := ""
			if unsafeName == "link" {
				entryType = "symlink"
			}
			ui := uiArchive(t, map[string][]byte{"index.html": []byte("ok"), unsafeName: []byte("bad")}, entryType)
			release := artifactRelease([]byte("center"), []byte("node"), ui)
			downloader := &memoryDownloader{assets: map[int64][]byte{2: []byte("center"), 3: []byte("node"), 4: ui}}
			if _, err := store.Install(context.Background(), release, "", downloader); err == nil {
				t.Fatal("Install() accepted unsafe UI archive")
			}
			paths, _ := store.Paths(release.Manifest.ID, release.Manifest.Version)
			if _, err := os.Stat(paths.Root); !os.IsNotExist(err) {
				t.Fatalf("failed release remains: %v", err)
			}
		})
	}
}

func TestPluginQuarantineRestoresOrRemovesAllState(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join(t.TempDir(), "plugins"))
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	pluginID := "io.relayward.test"
	paths, _ := store.Paths(pluginID, "1.2.3")
	data, _ := store.DataDirectory(pluginID)
	runtime, _ := store.RuntimeDirectory(pluginID)
	for _, path := range []string{paths.Root, data, runtime} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	quarantined, err := store.QuarantinePlugin(pluginID)
	if err != nil {
		t.Fatalf("QuarantinePlugin() error = %v", err)
	}
	for _, path := range []string{paths.Root, data, runtime} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("quarantined path %q remains: %v", path, err)
		}
	}
	if err := quarantined.Restore(); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	for _, path := range []string{paths.Root, data, runtime} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("restored path %q = %+v, %v", path, info, err)
		}
	}
	quarantined, err = store.QuarantinePlugin(pluginID)
	if err != nil {
		t.Fatalf("second QuarantinePlugin() error = %v", err)
	}
	if err := quarantined.Remove(); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	for _, path := range []string{paths.Root, data, runtime} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed path %q remains: %v", path, err)
		}
	}
}

func artifactRelease(center, node, ui []byte) githubrelease.Release {
	agentAPI, uiAPI := uint32(1), uint32(1)
	declarations := []manifest.Artifact{
		testArtifact(manifest.ArtifactCenter, "center", center, "linux", "amd64"),
		testArtifact(manifest.ArtifactNode, "node", node, "linux", "amd64"),
		testArtifact(manifest.ArtifactUI, "ui.tar.gz", ui, "", ""),
	}
	return githubrelease.Release{
		ID: 10, Repository: githubrelease.Repository{Owner: "Relayward", Name: "plugin"}, Tag: "v1.2.3",
		Manifest: manifest.Manifest{
			APIVersion: "relayward.plugin/v1", ID: "io.relayward.test", Name: "Test", Version: "1.2.3",
			Kind: manifest.KindRuntime, Requires: manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI, UIAPI: &uiAPI},
			Permissions: []manifest.Permission{}, Artifacts: declarations,
		},
		Assets: map[manifest.ArtifactRole]githubrelease.Asset{
			manifest.ArtifactCenter: {ID: 2, Name: "center", Size: int64(len(center))},
			manifest.ArtifactNode:   {ID: 3, Name: "node", Size: int64(len(node))},
			manifest.ArtifactUI:     {ID: 4, Name: "ui.tar.gz", Size: int64(len(ui))},
		},
	}
}

func testArtifact(role manifest.ArtifactRole, name string, raw []byte, osName, arch string) manifest.Artifact {
	digest := sha256.Sum256(raw)
	return manifest.Artifact{Role: role, File: name, Size: int64(len(raw)), SHA256: hex.EncodeToString(digest[:]), OS: osName, Arch: arch}
}

func uiArchive(t *testing.T, files map[string][]byte, special string) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for name, raw := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(raw)), Typeflag: tar.TypeReg}
		if special == "symlink" && name == "link" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = "index.html"
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := archive.Write(raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
