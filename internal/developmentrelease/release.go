package developmentrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
)

type Upstream interface {
	Inspect(context.Context, string, string, string) (githubrelease.Release, error)
	ListStableVersions(context.Context, string, string) ([]githubrelease.ReleaseVersion, error)
	DownloadAsset(context.Context, githubrelease.Repository, githubrelease.Asset, string, string, io.Writer) error
	ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error)
}

type Client struct {
	upstream  Upstream
	directory string
	release   githubrelease.Release
}

func Open(directory, rawRepository string, upstream Upstream) (*Client, error) {
	if upstream == nil {
		return nil, errors.New("development release upstream is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve development release directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, errors.New("development release directory is unavailable")
	}
	repository, err := githubrelease.ParseRepository(rawRepository)
	if err != nil {
		return nil, fmt.Errorf("development release repository: %w", err)
	}
	manifestFile, err := os.Open(filepath.Join(absolute, githubrelease.ManifestAssetName))
	if err != nil {
		return nil, fmt.Errorf("open development plugin manifest: %w", err)
	}
	value, decodeErr := manifest.Decode(manifestFile)
	closeErr := manifestFile.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("decode development plugin manifest: %w", decodeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close development plugin manifest: %w", closeErr)
	}
	if err := manifest.CheckCompatibility(value, contract.SupportedAPIs{
		Control: []uint32{contract.ControlAPIMajor}, Agent: []uint32{contract.AgentAPIMajor}, UI: []uint32{contract.UIAPIMajor},
	}); err != nil {
		return nil, fmt.Errorf("development plugin is incompatible: %w", err)
	}
	assets := make(map[manifest.ArtifactRole]githubrelease.Asset, len(value.Artifacts))
	for index, artifact := range value.Artifacts {
		file, err := openArtifact(absolute, artifact.File)
		if err != nil {
			return nil, fmt.Errorf("inspect development %s artifact: %w", artifact.Role, err)
		}
		assetInfo, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil {
			return nil, fmt.Errorf("inspect development %s artifact: %w", artifact.Role, statErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close development %s artifact: %w", artifact.Role, closeErr)
		}
		if assetInfo.Size() != artifact.Size {
			return nil, fmt.Errorf("development %s artifact size does not match the manifest", artifact.Role)
		}
		assets[artifact.Role] = githubrelease.Asset{ID: int64(index + 1), Name: artifact.File, Size: artifact.Size}
	}
	return &Client{
		upstream: upstream, directory: absolute,
		release: githubrelease.Release{
			ID: 1, Repository: repository, Tag: "v" + value.Version, Manifest: value, Assets: assets,
		},
	}, nil
}

func (client *Client) Release() githubrelease.Release {
	return client.release
}

func (client *Client) Inspect(ctx context.Context, repository, version, token string) (githubrelease.Release, error) {
	if repository == client.release.Repository.URL() && version == client.release.Manifest.Version {
		if token != "" {
			return githubrelease.Release{}, errors.New("development release does not accept a GitHub token")
		}
		return client.release, nil
	}
	return client.upstream.Inspect(ctx, repository, version, token)
}

func (client *Client) ListStableVersions(ctx context.Context, repository, token string) ([]githubrelease.ReleaseVersion, error) {
	return client.upstream.ListStableVersions(ctx, repository, token)
}

func (client *Client) DownloadAsset(ctx context.Context, repository githubrelease.Repository, asset githubrelease.Asset,
	token, expectedSHA256 string, destination io.Writer,
) error {
	if repository == client.release.Repository {
		for _, local := range client.release.Assets {
			if asset == local {
				if token != "" {
					return errors.New("development release does not accept a GitHub token")
				}
				return client.download(ctx, local, expectedSHA256, destination)
			}
		}
	}
	return client.upstream.DownloadAsset(ctx, repository, asset, token, expectedSHA256, destination)
}

func (client *Client) ResolveAssetURL(ctx context.Context, repository githubrelease.Repository, assetID int64, token string) (string, error) {
	return client.upstream.ResolveAssetURL(ctx, repository, assetID, token)
}

func (client *Client) download(ctx context.Context, asset githubrelease.Asset, expectedSHA256 string, destination io.Writer) error {
	file, err := openArtifact(client.directory, asset.Name)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := copyWithContext(ctx, io.MultiWriter(destination, hash), io.LimitReader(file, asset.Size+1))
	if err != nil {
		return fmt.Errorf("read development release artifact: %w", err)
	}
	if written != asset.Size {
		return errors.New("development release artifact size does not match the manifest")
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expectedSHA256 {
		return errors.New("development release artifact SHA-256 does not match the manifest")
	}
	return nil
}

func openArtifact(directory, name string) (*os.File, error) {
	if name == "" || filepath.Base(name) != name {
		return nil, errors.New("development artifact name must be a plain file name")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("development artifact is not a regular file")
	}
	return file, nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			output, writeErr := destination.Write(buffer[:count])
			written += int64(output)
			if writeErr != nil {
				return written, writeErr
			}
			if output != count {
				return written, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
