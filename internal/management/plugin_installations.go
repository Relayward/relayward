package management

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

type pluginReleaseClient interface {
	pluginartifact.Downloader
	Inspect(context.Context, string, string, string) (githubrelease.Release, error)
	ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error)
}

type pluginArtifactStore interface {
	Install(context.Context, githubrelease.Release, string, pluginartifact.Downloader) (pluginartifact.Paths, error)
	Verify(manifest.Manifest) (pluginartifact.Paths, error)
	RemoveRelease(string, string) error
	QuarantinePlugin(string) (pluginartifact.QuarantinedPlugin, error)
	OpenUIFile(string, string, string) (*os.File, os.FileInfo, error)
}

type centerPluginRuntime interface {
	Switch(context.Context, store.PluginVersion) error
	Rollback(context.Context, string, *store.PluginVersion) error
	StopPlugin(context.Context, string) error
	InvokeUI(context.Context, string, string, []byte) ([]byte, error)
	RenderSubscription(context.Context, string, *centerpluginv1.RenderSubscriptionRequest) (*centerpluginv1.RenderSubscriptionResponse, error)
}

type PluginReleaseInput struct {
	Repository          string
	Version             string
	GitHubToken         string
	ApprovedPermissions []string
}

type PluginReleaseCandidate struct {
	Repository string
	ReleaseID  int64
	Tag        string
	Manifest   manifest.Manifest
	Update     bool
}

func (service *Service) ConfigurePluginLifecycle(releases pluginReleaseClient, artifacts pluginArtifactStore, runtime centerPluginRuntime) error {
	if releases == nil || artifacts == nil || runtime == nil {
		return errors.New("complete plugin lifecycle dependencies are required")
	}
	service.pluginReleases = releases
	service.pluginArtifacts = artifacts
	service.pluginRuntime = runtime
	return nil
}

func (service *Service) InspectPluginRelease(ctx context.Context, input PluginReleaseInput) (PluginReleaseCandidate, error) {
	if err := service.requirePluginLifecycle(); err != nil {
		return PluginReleaseCandidate{}, err
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	release, _, existing, err := service.inspectPluginRelease(ctx, input)
	if err != nil {
		return PluginReleaseCandidate{}, err
	}
	return PluginReleaseCandidate{
		Repository: release.Repository.URL(), ReleaseID: release.ID, Tag: release.Tag,
		Manifest: release.Manifest, Update: existing != nil,
	}, nil
}

func (service *Service) InstallPluginRelease(ctx context.Context, input PluginReleaseInput) (store.PluginInstallation, error) {
	if err := service.requirePluginLifecycle(); err != nil {
		return store.PluginInstallation{}, err
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	release, token, existing, err := service.inspectPluginRelease(ctx, input)
	if err != nil {
		return store.PluginInstallation{}, err
	}
	approved := append([]string(nil), input.ApprovedPermissions...)
	sort.Strings(approved)
	if hasDuplicateStrings(approved) || !permissionsMatch(release.Manifest, approved) {
		return store.PluginInstallation{}, invalid("approved_permissions", "must approve every permission declared by the release")
	}
	if existing != nil && existing.ActiveVersion == release.Manifest.Version && existing.State == "active" {
		return store.PluginInstallation{}, invalid("version", "is already active")
	}
	newRelease := true
	if _, err := service.pluginArtifacts.Install(ctx, release, token, service.pluginReleases); err != nil {
		if !errors.Is(err, pluginartifact.ErrReleaseExists) {
			return store.PluginInstallation{}, invalid("release", "artifacts could not be installed or verified")
		}
		newRelease = false
		if _, verifyErr := service.pluginArtifacts.Verify(release.Manifest); verifyErr != nil {
			return store.PluginInstallation{}, invalid("release", "the existing release artifacts failed verification")
		}
	}
	version, err := pluginVersionFromRelease(release, approved)
	if err != nil {
		if newRelease {
			_ = service.pluginArtifacts.RemoveRelease(release.Manifest.ID, release.Manifest.Version)
		}
		return store.PluginInstallation{}, err
	}
	var tokenCiphertext []byte
	if input.GitHubToken != "" {
		if service.secrets == nil || !service.secrets.Available() {
			if newRelease {
				_ = service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version)
			}
			return store.PluginInstallation{}, secretbox.ErrUnavailable
		}
		tokenCiphertext, err = service.secrets.Encrypt(
			store.PluginInstallationSecretOwnerType, version.PluginID, store.PluginInstallationGitHubToken,
			[]byte(input.GitHubToken),
		)
		if err != nil {
			if newRelease {
				_ = service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version)
			}
			return store.PluginInstallation{}, fmt.Errorf("encrypt GitHub token: %w", err)
		}
	}
	var previous *store.PluginVersion
	if existing != nil && existing.ActiveVersion != "" {
		value, err := service.store.PluginVersionByID(ctx, existing.PluginID, existing.ActiveVersion)
		if err != nil {
			if newRelease {
				_ = service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version)
			}
			return store.PluginInstallation{}, err
		}
		previous = &value
	}
	if err := service.pluginRuntime.Switch(ctx, version); err != nil {
		if newRelease {
			_ = service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version)
		}
		return store.PluginInstallation{}, invalid("release", "center plugin activation or health checks failed")
	}
	installation := store.PluginInstallation{
		PluginID: version.PluginID, Repository: release.Repository.URL(), Kind: string(version.Manifest.Kind),
		DesiredVersion: version.Version, ActiveVersion: version.Version, Manifest: version.Manifest,
		ApprovedPermissions: approved,
	}
	committed, err := service.store.CommitPluginRelease(ctx, installation, version, tokenCiphertext, service.currentTime())
	if err == nil {
		return committed, nil
	}
	rollbackErr := service.rollbackPluginActivation(version.PluginID, previous)
	if newRelease {
		rollbackErr = errors.Join(rollbackErr, service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version))
	}
	return store.PluginInstallation{}, errors.Join(err, rollbackErr)
}

func (service *Service) ListPluginInstallations(ctx context.Context) ([]store.PluginInstallation, error) {
	return service.store.ListPluginInstallations(ctx)
}

func (service *Service) PluginInstallation(ctx context.Context, pluginID string) (store.PluginInstallation, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return store.PluginInstallation{}, invalid("plugin_id", err.Error())
	}
	return service.store.PluginInstallationByID(ctx, pluginID)
}

func (service *Service) UninstallPlugin(ctx context.Context, pluginID string) error {
	if err := service.requirePluginLifecycle(); err != nil {
		return err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return invalid("plugin_id", err.Error())
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	installation, err := service.store.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return err
	}
	var previous *store.PluginVersion
	if installation.ActiveVersion != "" {
		version, err := service.store.PluginVersionByID(ctx, pluginID, installation.ActiveVersion)
		if err != nil {
			return err
		}
		previous = &version
	}
	if err := service.pluginRuntime.StopPlugin(ctx, pluginID); err != nil {
		return fmt.Errorf("stop center plugin: %w", err)
	}
	quarantined, err := service.pluginArtifacts.QuarantinePlugin(pluginID)
	if err != nil {
		return errors.Join(err, service.rollbackPluginActivation(pluginID, previous))
	}
	if _, err := service.store.DeletePluginInstallation(ctx, pluginID, service.currentTime()); err != nil {
		restoreErr := quarantined.Restore()
		restartErr := service.rollbackPluginActivation(pluginID, previous)
		return errors.Join(err, restoreErr, restartErr)
	}
	if err := quarantined.Remove(); err != nil {
		return fmt.Errorf("finish plugin file cleanup: %w", err)
	}
	return nil
}

func (service *Service) InvokePluginUI(ctx context.Context, pluginID, method string, raw []byte) ([]byte, error) {
	if err := service.requirePluginLifecycle(); err != nil {
		return nil, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return nil, invalid("plugin_id", err.Error())
	}
	installation, err := service.store.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return nil, err
	}
	if installation.State != "active" || installation.Health != "healthy" {
		return nil, ErrUpstreamUnavailable
	}
	response, err := service.pluginRuntime.InvokeUI(ctx, pluginID, method, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: plugin UI RPC failed", ErrUpstreamUnavailable)
	}
	return response, nil
}

func (service *Service) OpenPluginUIFile(ctx context.Context, pluginID, name string) (*os.File, os.FileInfo, error) {
	if err := service.requirePluginLifecycle(); err != nil {
		return nil, nil, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return nil, nil, invalid("plugin_id", err.Error())
	}
	installation, err := service.store.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return nil, nil, err
	}
	if installation.State != "active" || installation.ActiveVersion == "" || !hasPluginUI(installation.Manifest) {
		return nil, nil, store.ErrNotFound
	}
	file, info, err := service.pluginArtifacts.OpenUIFile(pluginID, installation.ActiveVersion, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, store.ErrNotFound
		}
		if errors.Is(err, pluginartifact.ErrInvalidUIPath) {
			return nil, nil, invalid("path", "must identify a regular plugin UI file")
		}
		return nil, nil, err
	}
	return file, info, nil
}

func (service *Service) inspectPluginRelease(ctx context.Context, input PluginReleaseInput) (githubrelease.Release, string, *store.PluginInstallation, error) {
	repository, err := githubrelease.ParseRepository(input.Repository)
	if err != nil {
		return githubrelease.Release{}, "", nil, invalid("repository", err.Error())
	}
	canonicalRepository := repository.URL()
	var existingByRepository *store.PluginInstallation
	if value, err := service.store.PluginInstallationByRepository(ctx, canonicalRepository); err == nil {
		existingByRepository = &value
	} else if !errors.Is(err, store.ErrNotFound) {
		return githubrelease.Release{}, "", nil, err
	}
	token := input.GitHubToken
	if token == "" && existingByRepository != nil {
		token, err = service.decryptPluginGitHubToken(ctx, existingByRepository.PluginID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return githubrelease.Release{}, "", nil, err
		}
	}
	release, err := service.pluginReleases.Inspect(ctx, canonicalRepository, input.Version, token)
	if err != nil {
		return githubrelease.Release{}, "", nil, translateGitHubReleaseError(err)
	}
	for _, permission := range release.Manifest.Permissions {
		if permission.Name != centerpluginv1.PermissionNodesRead && permission.Name != centerpluginv1.PermissionServicesWrite {
			return githubrelease.Release{}, "", nil, invalid("repository", "release requests an unsupported permission: "+permission.Name)
		}
	}
	existing, err := service.store.PluginInstallationByID(ctx, release.Manifest.ID)
	if errors.Is(err, store.ErrNotFound) {
		existing = store.PluginInstallation{}
	} else if err != nil {
		return githubrelease.Release{}, "", nil, err
	} else if existing.Repository != canonicalRepository {
		return githubrelease.Release{}, "", nil, store.ErrConflict
	}
	if existingByRepository != nil && existingByRepository.PluginID != release.Manifest.ID {
		return githubrelease.Release{}, "", nil, store.ErrConflict
	}
	if existing.PluginID == "" {
		return release, token, nil, nil
	}
	return release, token, &existing, nil
}

func (service *Service) decryptPluginGitHubToken(ctx context.Context, pluginID string) (string, error) {
	ciphertext, err := service.store.Secret(
		ctx, store.PluginInstallationSecretOwnerType, pluginID, store.PluginInstallationGitHubToken,
	)
	if err != nil {
		return "", err
	}
	if service.secrets == nil || !service.secrets.Available() {
		return "", secretbox.ErrUnavailable
	}
	plaintext, err := service.secrets.Decrypt(
		store.PluginInstallationSecretOwnerType, pluginID, store.PluginInstallationGitHubToken, ciphertext,
	)
	if err != nil {
		return "", fmt.Errorf("decrypt GitHub token: %w", err)
	}
	return string(plaintext), nil
}

func (service *Service) rollbackPluginActivation(pluginID string, previous *store.PluginVersion) error {
	rollbackContext, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return service.pluginRuntime.Rollback(rollbackContext, pluginID, previous)
}

func (service *Service) requirePluginLifecycle() error {
	if service.pluginReleases == nil || service.pluginArtifacts == nil || service.pluginRuntime == nil {
		return errors.New("plugin lifecycle is not configured")
	}
	return nil
}

func pluginVersionFromRelease(release githubrelease.Release, approved []string) (store.PluginVersion, error) {
	center, ok := release.Assets[manifest.ArtifactCenter]
	if !ok {
		return store.PluginVersion{}, errors.New("center plugin asset is missing")
	}
	value := store.PluginVersion{
		PluginID: release.Manifest.ID, Version: release.Manifest.Version, ReleaseID: release.ID,
		ReleaseTag: release.Tag, Manifest: release.Manifest,
		ApprovedPermissions: append([]string(nil), approved...), CenterAssetID: center.ID,
	}
	if asset, exists := release.Assets[manifest.ArtifactNode]; exists {
		id := asset.ID
		value.NodeAssetID = &id
	}
	if asset, exists := release.Assets[manifest.ArtifactUI]; exists {
		id := asset.ID
		value.UIAssetID = &id
	}
	return value, nil
}

func permissionsMatch(value manifest.Manifest, approved []string) bool {
	wanted := make([]string, len(value.Permissions))
	for index, permission := range value.Permissions {
		wanted[index] = permission.Name
	}
	sort.Strings(wanted)
	if len(wanted) != len(approved) {
		return false
	}
	for index := range wanted {
		if wanted[index] != approved[index] {
			return false
		}
	}
	return true
}

func hasDuplicateStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}

func hasPluginUI(value manifest.Manifest) bool {
	for _, artifact := range value.Artifacts {
		if artifact.Role == manifest.ArtifactUI {
			return true
		}
	}
	return false
}

func translateGitHubReleaseError(err error) error {
	switch {
	case errors.Is(err, githubrelease.ErrUnauthorized):
		return invalid("github_token", "does not authorize access to the repository")
	case errors.Is(err, githubrelease.ErrNotFound):
		return invalid("repository", "release was not found or is not accessible")
	case errors.Is(err, githubrelease.ErrRateLimited):
		return ErrUpstreamUnavailable
	default:
		return invalid("release", "could not be read or validated")
	}
}
