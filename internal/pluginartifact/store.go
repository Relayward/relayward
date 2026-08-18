package pluginartifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
)

const (
	maximumUIFiles             = 4096
	maximumUIUncompressedBytes = 128 << 20
)

var (
	ErrReleaseExists = errors.New("plugin release already exists")
	ErrInvalidUIPath = errors.New("invalid plugin UI path")
)

type Downloader interface {
	DownloadAsset(context.Context, githubrelease.Repository, githubrelease.Asset, string, string, io.Writer) error
}

type Paths struct {
	Root       string
	Executable string
	Node       string
	UI         string
	UIArchive  string
	Manifest   string
}

type Store struct {
	root          string
	releasesDir   string
	stagingDir    string
	dataDir       string
	runtimeDir    string
	quarantineDir string
}

type QuarantinedPlugin interface {
	Restore() error
	Remove() error
}

type quarantine struct {
	root  string
	moves []quarantineMove
}

type quarantineMove struct {
	source      string
	destination string
}

func Open(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("plugin artifact root must be absolute")
	}
	store := &Store{
		root: root, releasesDir: filepath.Join(root, "releases"), stagingDir: filepath.Join(root, "staging"),
		dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime"),
		quarantineDir: filepath.Join(root, "quarantine"),
	}
	for _, directory := range []string{
		store.root, store.releasesDir, store.stagingDir, store.dataDir, store.runtimeDir, store.quarantineDir,
	} {
		if err := protectDirectory(directory); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) Install(ctx context.Context, release githubrelease.Release, token string, downloader Downloader) (Paths, error) {
	if downloader == nil {
		return Paths{}, errors.New("plugin artifact downloader is required")
	}
	if err := manifest.Validate(release.Manifest); err != nil {
		return Paths{}, fmt.Errorf("validate plugin release: %w", err)
	}
	if release.Repository.Owner == "" || release.Repository.Name == "" {
		return Paths{}, errors.New("plugin release repository is required")
	}
	paths, err := store.Paths(release.Manifest.ID, release.Manifest.Version)
	if err != nil {
		return Paths{}, err
	}
	if _, err := os.Lstat(paths.Root); err == nil {
		return Paths{}, ErrReleaseExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return Paths{}, fmt.Errorf("inspect plugin release directory: %w", err)
	}
	pluginReleases := filepath.Dir(paths.Root)
	if err := protectDirectory(pluginReleases); err != nil {
		return Paths{}, err
	}
	staging, err := os.MkdirTemp(store.stagingDir, ".plugin-release-")
	if err != nil {
		return Paths{}, fmt.Errorf("create plugin release staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	if err := os.Chmod(staging, 0o700); err != nil {
		return Paths{}, fmt.Errorf("protect plugin release staging directory: %w", err)
	}

	centerDeclaration, centerAsset, ok := releaseArtifact(release, manifest.ArtifactCenter)
	if !ok {
		return Paths{}, errors.New("plugin release does not contain a center artifact")
	}
	centerPath := filepath.Join(staging, "center")
	if err := downloadFile(ctx, centerPath, 0o700, release, centerAsset, centerDeclaration.SHA256, token, downloader); err != nil {
		return Paths{}, fmt.Errorf("install center plugin artifact: %w", err)
	}
	if nodeDeclaration, nodeAsset, exists := releaseArtifact(release, manifest.ArtifactNode); exists {
		nodePath := filepath.Join(staging, "node")
		if err := downloadFile(ctx, nodePath, 0o700, release, nodeAsset, nodeDeclaration.SHA256, token, downloader); err != nil {
			return Paths{}, fmt.Errorf("install node plugin artifact: %w", err)
		}
	}
	if uiDeclaration, uiAsset, exists := releaseArtifact(release, manifest.ArtifactUI); exists {
		archivePath := filepath.Join(staging, ".ui.tar.gz")
		if err := downloadFile(ctx, archivePath, 0o600, release, uiAsset, uiDeclaration.SHA256, token, downloader); err != nil {
			return Paths{}, fmt.Errorf("install plugin UI artifact: %w", err)
		}
		uiDirectory := filepath.Join(staging, "ui")
		if err := extractUIArchive(archivePath, uiDirectory); err != nil {
			return Paths{}, err
		}
		if err := os.Rename(archivePath, filepath.Join(staging, "ui.tar.gz")); err != nil {
			return Paths{}, fmt.Errorf("retain verified plugin UI archive: %w", err)
		}
	}
	manifestJSON, err := json.Marshal(release.Manifest)
	if err != nil {
		return Paths{}, fmt.Errorf("encode installed plugin manifest: %w", err)
	}
	if err := writeDurableFile(filepath.Join(staging, "manifest.json"), manifestJSON, 0o600); err != nil {
		return Paths{}, err
	}
	if err := syncDirectory(staging); err != nil {
		return Paths{}, err
	}
	if err := os.Rename(staging, paths.Root); err != nil {
		if _, statErr := os.Lstat(paths.Root); statErr == nil {
			return Paths{}, ErrReleaseExists
		}
		return Paths{}, fmt.Errorf("publish plugin release: %w", err)
	}
	if err := syncDirectory(pluginReleases); err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func (store *Store) Paths(pluginID, version string) (Paths, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return Paths{}, fmt.Errorf("plugin ID: %w", err)
	}
	if err := contract.ValidateSemanticVersion(version); err != nil {
		return Paths{}, fmt.Errorf("plugin version: %w", err)
	}
	root := filepath.Join(store.releasesDir, pluginID, version)
	return Paths{
		Root: root, Executable: filepath.Join(root, "center"), Node: filepath.Join(root, "node"), UI: filepath.Join(root, "ui"),
		UIArchive: filepath.Join(root, "ui.tar.gz"), Manifest: filepath.Join(root, "manifest.json"),
	}, nil
}

func (store *Store) Verify(value manifest.Manifest) (Paths, error) {
	if err := manifest.Validate(value); err != nil {
		return Paths{}, err
	}
	paths, err := store.Paths(value.ID, value.Version)
	if err != nil {
		return Paths{}, err
	}
	rawManifest, err := os.ReadFile(paths.Manifest)
	if err != nil {
		return Paths{}, errors.New("installed plugin manifest is unavailable")
	}
	var stored manifest.Manifest
	if err := json.Unmarshal(rawManifest, &stored); err != nil {
		return Paths{}, errors.New("installed plugin manifest is invalid")
	}
	wanted, _ := json.Marshal(value)
	actual, _ := json.Marshal(stored)
	if !bytes.Equal(wanted, actual) {
		return Paths{}, errors.New("installed plugin manifest does not match the release")
	}
	for _, artifact := range value.Artifacts {
		switch artifact.Role {
		case manifest.ArtifactCenter:
			if err := verifyFile(paths.Executable, artifact, true); err != nil {
				return Paths{}, err
			}
		case manifest.ArtifactNode:
			if err := verifyFile(paths.Node, artifact, true); err != nil {
				return Paths{}, err
			}
		case manifest.ArtifactUI:
			if err := verifyFile(paths.UIArchive, artifact, false); err != nil {
				return Paths{}, err
			}
			info, err := os.Lstat(filepath.Join(paths.UI, "index.html"))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
				return Paths{}, errors.New("installed plugin UI entry point failed verification")
			}
		}
	}
	return paths, nil
}

func (store *Store) DataDirectory(pluginID string) (string, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return "", err
	}
	path := filepath.Join(store.dataDir, pluginID)
	if err := protectDirectory(path); err != nil {
		return "", err
	}
	return path, nil
}

func (store *Store) RuntimeDirectory(pluginID string) (string, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return "", err
	}
	path := filepath.Join(store.runtimeDir, pluginID)
	if err := protectDirectory(path); err != nil {
		return "", err
	}
	return path, nil
}

func (store *Store) RemoveRelease(pluginID, version string) error {
	paths, err := store.Paths(pluginID, version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("remove plugin release: %w", err)
	}
	return syncDirectory(filepath.Dir(paths.Root))
}

func (store *Store) OpenNodeFile(pluginID, version string) (*os.File, os.FileInfo, error) {
	paths, err := store.Paths(pluginID, version)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(paths.Node)
	if err != nil {
		return nil, nil, fmt.Errorf("open plugin node artifact: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("inspect plugin node artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
		file.Close()
		return nil, nil, errors.New("plugin node artifact failed verification")
	}
	return file, info, nil
}

func (store *Store) OpenUIFile(pluginID, version, name string) (*os.File, os.FileInfo, error) {
	paths, err := store.Paths(pluginID, version)
	if err != nil {
		return nil, nil, err
	}
	if name == "" || name != path.Clean(name) || path.IsAbs(name) || name == "." || name == ".." ||
		strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
		return nil, nil, ErrInvalidUIPath
	}
	root, err := os.OpenRoot(paths.UI)
	if err != nil {
		return nil, nil, fmt.Errorf("open plugin UI directory: %w", err)
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, nil, fmt.Errorf("open plugin UI file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("inspect plugin UI file: %w", err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.New("plugin UI path is not a regular file")
	}
	return file, info, nil
}

func (store *Store) QuarantinePlugin(pluginID string) (QuarantinedPlugin, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(store.quarantineDir, "."+pluginID+"-")
	if err != nil {
		return nil, fmt.Errorf("create plugin quarantine: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("protect plugin quarantine: %w", err)
	}
	value := &quarantine{root: root}
	sources := []struct {
		name string
		path string
	}{
		{name: "releases", path: filepath.Join(store.releasesDir, pluginID)},
		{name: "data", path: filepath.Join(store.dataDir, pluginID)},
		{name: "runtime", path: filepath.Join(store.runtimeDir, pluginID)},
	}
	for _, source := range sources {
		if _, err := os.Lstat(source.path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, errors.Join(fmt.Errorf("inspect plugin files before uninstall: %w", err), value.Restore())
		}
		move := quarantineMove{source: source.path, destination: filepath.Join(root, source.name)}
		if err := os.Rename(move.source, move.destination); err != nil {
			return nil, errors.Join(fmt.Errorf("quarantine plugin files: %w", err), value.Restore())
		}
		value.moves = append(value.moves, move)
		if err := syncDirectory(filepath.Dir(move.source)); err != nil {
			return nil, errors.Join(err, value.Restore())
		}
	}
	if err := syncDirectory(root); err != nil {
		return nil, errors.Join(err, value.Restore())
	}
	return value, nil
}

func (value *quarantine) Restore() error {
	if value == nil || value.root == "" {
		return nil
	}
	var result error
	for index := len(value.moves) - 1; index >= 0; index-- {
		move := value.moves[index]
		if _, err := os.Lstat(move.destination); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			result = errors.Join(result, fmt.Errorf("inspect quarantined plugin files: %w", err))
			continue
		}
		if _, err := os.Lstat(move.source); err == nil {
			result = errors.Join(result, fmt.Errorf("restore plugin files: destination %q already exists", move.source))
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("inspect plugin restore destination: %w", err))
			continue
		}
		if err := os.Rename(move.destination, move.source); err != nil {
			result = errors.Join(result, fmt.Errorf("restore plugin files: %w", err))
			continue
		}
		if err := syncDirectory(filepath.Dir(move.source)); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return result
	}
	return value.Remove()
}

func (value *quarantine) Remove() error {
	if value == nil || value.root == "" {
		return nil
	}
	root := value.root
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove plugin quarantine: %w", err)
	}
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		return err
	}
	value.root = ""
	value.moves = nil
	return nil
}

func releaseArtifact(release githubrelease.Release, role manifest.ArtifactRole) (manifest.Artifact, githubrelease.Asset, bool) {
	for _, declaration := range release.Manifest.Artifacts {
		if declaration.Role == role {
			asset, exists := release.Assets[role]
			return declaration, asset, exists
		}
	}
	return manifest.Artifact{}, githubrelease.Asset{}, false
}

func downloadFile(ctx context.Context, path string, mode os.FileMode, release githubrelease.Release, asset githubrelease.Asset,
	digest, token string, downloader Downloader,
) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create staged artifact: %w", err)
	}
	succeeded := false
	defer func() {
		file.Close()
		if !succeeded {
			os.Remove(path)
		}
	}()
	if err := downloader.DownloadAsset(ctx, release.Repository, asset, token, digest, file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync staged artifact: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("protect staged artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close staged artifact: %w", err)
	}
	succeeded = true
	return nil
}

func extractUIArchive(archivePath, destination string) error {
	if err := protectDirectory(destination); err != nil {
		return err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open plugin UI archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return errors.New("plugin UI artifact is not a gzip archive")
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := 0
	var total int64
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("read plugin UI archive")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := protectDirectory(target); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			files++
			if files > maximumUIFiles || header.Size < 0 || total > maximumUIUncompressedBytes-header.Size {
				return errors.New("plugin UI archive exceeds extraction limits")
			}
			total += header.Size
			if err := protectDirectory(filepath.Dir(target)); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("create plugin UI file: %w", err)
			}
			written, copyErr := io.Copy(output, io.LimitReader(archive, header.Size+1))
			syncErr := output.Sync()
			closeErr := output.Close()
			if copyErr != nil || written != header.Size || syncErr != nil || closeErr != nil {
				return errors.New("write plugin UI file")
			}
		default:
			return fmt.Errorf("plugin UI archive contains unsupported entry type %d", header.Typeflag)
		}
	}
	indexPath := filepath.Join(destination, "index.html")
	info, err := os.Lstat(indexPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("plugin UI archive must contain a regular index.html")
	}
	return syncDirectory(destination)
}

func safeArchivePath(value string) (string, error) {
	if value == "" || len(value) > 240 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", errors.New("plugin UI archive contains an invalid path")
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != strings.TrimSuffix(value, "/") {
		return "", errors.New("plugin UI archive contains an unsafe path")
	}
	return clean, nil
}

func protectDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create plugin directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect plugin directory: %w", err)
	}
	return nil
}

func writeDurableFile(path string, raw []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create plugin metadata: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return fmt.Errorf("write plugin metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync plugin metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close plugin metadata: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open plugin directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync plugin directory: %w", err)
	}
	return nil
}

func verifyFile(path string, declaration manifest.Artifact, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != declaration.Size || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("installed plugin artifact %q failed metadata verification", declaration.File)
	}
	if executable && info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("installed plugin artifact %q is not executable", declaration.File)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open installed plugin artifact %q", declaration.File)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != declaration.SHA256 {
		return fmt.Errorf("installed plugin artifact %q failed SHA-256 verification", declaration.File)
	}
	return nil
}
