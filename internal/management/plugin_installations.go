package management

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
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
	ListStableVersions(context.Context, string, string) ([]githubrelease.ReleaseVersion, error)
	ResolveAssetURL(context.Context, githubrelease.Repository, int64, string) (string, error)
}

type pluginArtifactStore interface {
	Install(context.Context, githubrelease.Release, string, pluginartifact.Downloader) (pluginartifact.Paths, error)
	Verify(manifest.Manifest) (pluginartifact.Paths, error)
	RemoveRelease(string, string) error
	QuarantinePlugin(string) (pluginartifact.QuarantinedPlugin, error)
	OpenNodeFile(string, string) (*os.File, os.FileInfo, error)
	OpenUIFile(string, string, string) (*os.File, os.FileInfo, error)
}

type centerPluginRuntime interface {
	Switch(context.Context, store.PluginVersion) (bool, error)
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

type PluginReleaseVersion struct {
	Tag         string
	Version     string
	PublishedAt time.Time
}

type developmentPluginRelease struct {
	release   githubrelease.Release
	publicURL string
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

func (service *Service) ConfigureDevelopmentPluginRelease(release githubrelease.Release, publicURL string) error {
	if err := service.requirePluginLifecycle(); err != nil {
		return err
	}
	if err := manifest.Validate(release.Manifest); err != nil {
		return fmt.Errorf("validate development plugin manifest: %w", err)
	}
	if release.ID < 1 || release.Tag != "v"+release.Manifest.Version ||
		release.Repository.Owner == "" || release.Repository.Name == "" {
		return errors.New("development plugin release metadata is invalid")
	}
	for _, artifact := range release.Manifest.Artifacts {
		asset, exists := release.Assets[artifact.Role]
		if !exists || asset.ID < 1 || asset.Name != artifact.File || asset.Size != artifact.Size {
			return errors.New("development plugin release assets do not match its manifest")
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("development public URL must be an HTTPS origin without a path, query, fragment, or credentials")
	}
	service.developmentPlugin = &developmentPluginRelease{release: release, publicURL: parsed.String()}
	return nil
}

func (service *Service) EnsureDevelopmentPluginRelease(ctx context.Context) (store.PluginInstallation, error) {
	if service.developmentPlugin == nil {
		return store.PluginInstallation{}, errors.New("development plugin release is not configured")
	}
	value := service.developmentPlugin.release
	existing, err := service.store.PluginInstallationByID(ctx, value.Manifest.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		existing = store.PluginInstallation{}
	case err != nil:
		return store.PluginInstallation{}, err
	case existing.Repository != value.Repository.URL():
		return store.PluginInstallation{}, store.ErrConflict
	}
	var installation store.PluginInstallation
	if existing.ActiveVersion == value.Manifest.Version && existing.State == "active" {
		if _, err := service.pluginArtifacts.Verify(value.Manifest); err != nil {
			return store.PluginInstallation{}, fmt.Errorf("verify active development plugin release: %w", err)
		}
		installation = existing
	} else {
		approved := make([]string, len(value.Manifest.Permissions))
		for index, permission := range value.Manifest.Permissions {
			approved[index] = permission.Name
		}
		installation, err = service.InstallPluginRelease(ctx, PluginReleaseInput{
			Repository: value.Repository.URL(), Version: value.Manifest.Version, ApprovedPermissions: approved,
		})
		if err != nil {
			return store.PluginInstallation{}, err
		}
	}
	return installation, nil
}

func (service *Service) ReconcileDevelopmentNodePlugins(ctx context.Context) (int, error) {
	if service.developmentPlugin == nil {
		return 0, errors.New("development plugin release is not configured")
	}
	value := service.developmentPlugin.release
	instances, err := service.ListNodePluginInstances(ctx)
	if err != nil {
		return 0, fmt.Errorf("list development node plugin instances: %w", err)
	}
	updated := 0
	for _, instance := range instances {
		if instance.PluginID != value.Manifest.ID || instance.DesiredState == agentv1.PluginStateAbsent ||
			instance.DesiredVersion == value.Manifest.Version {
			continue
		}
		if _, err := service.ReconcileNodePlugin(ctx, instance.NodeID, instance.PluginID, NodePluginInput{
			DesiredState: instance.DesiredState, Version: value.Manifest.Version,
		}); err != nil {
			return updated, fmt.Errorf("upgrade development node plugin on %s: %w", instance.NodeID, err)
		}
		updated++
	}
	return updated, nil
}

func (service *Service) OpenDevelopmentNodeArtifact(ctx context.Context, pluginID, version string) (*os.File, os.FileInfo, error) {
	if service.developmentPlugin == nil || pluginID != service.developmentPlugin.release.Manifest.ID ||
		version != service.developmentPlugin.release.Manifest.Version {
		return nil, nil, store.ErrNotFound
	}
	installation, err := service.store.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return nil, nil, err
	}
	if installation.State != "active" || installation.ActiveVersion != version || installation.Manifest.Version != version {
		return nil, nil, store.ErrNotFound
	}
	if _, err := service.pluginArtifacts.Verify(installation.Manifest); err != nil {
		return nil, nil, fmt.Errorf("verify development plugin artifacts: %w", err)
	}
	file, info, err := service.pluginArtifacts.OpenNodeFile(pluginID, version)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, store.ErrNotFound
	}
	return file, info, err
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

func (service *Service) ListPluginReleaseVersions(ctx context.Context, repository, githubToken string) ([]PluginReleaseVersion, error) {
	if err := service.requirePluginLifecycle(); err != nil {
		return nil, err
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	canonicalRepository, token, _, err := service.pluginRepositoryAccess(ctx, repository, githubToken)
	if err != nil {
		return nil, err
	}
	versions, err := service.pluginReleases.ListStableVersions(ctx, canonicalRepository, token)
	if err != nil {
		return nil, translateGitHubReleaseError(err)
	}
	result := make([]PluginReleaseVersion, len(versions))
	for index, version := range versions {
		result[index] = PluginReleaseVersion{
			Tag: version.Tag, Version: version.Version, PublishedAt: version.PublishedAt,
		}
	}
	return result, nil
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
			return store.PluginInstallation{}, invalidWithCause(
				"release", "artifacts could not be installed or verified", fmt.Errorf("install plugin artifacts: %w", err),
			)
		}
		newRelease = false
		if _, verifyErr := service.pluginArtifacts.Verify(release.Manifest); verifyErr != nil {
			return store.PluginInstallation{}, invalidWithCause(
				"release", "the existing release artifacts failed verification", fmt.Errorf("verify plugin artifacts: %w", verifyErr),
			)
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
	previousRestored, err := service.pluginRuntime.Switch(ctx, version)
	if err != nil {
		var rollbackErr error
		var rollbackSucceeded *bool
		if previous != nil {
			if !previousRestored {
				rollbackErr = service.rollbackPluginActivation(version.PluginID, previous)
				previousRestored = rollbackErr == nil
			}
			rollbackSucceeded = &previousRestored
		}
		auditErr := service.recordPluginReleaseFailure(
			version, release.Repository.URL(), existing, "activation", rollbackSucceeded,
		)
		var cleanupErr error
		if newRelease {
			cleanupErr = service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version)
		}
		return store.PluginInstallation{}, errors.Join(
			invalidWithCause(
				"release", "center plugin activation or health checks failed", fmt.Errorf("activate center plugin: %w", err),
			),
			rollbackErr, auditErr, cleanupErr,
		)
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
	rollbackSucceeded := rollbackErr == nil
	auditErr := service.recordPluginReleaseFailure(
		version, release.Repository.URL(), existing, "persistence", &rollbackSucceeded,
	)
	if newRelease {
		rollbackErr = errors.Join(rollbackErr, service.pluginArtifacts.RemoveRelease(version.PluginID, version.Version))
	}
	return store.PluginInstallation{}, errors.Join(err, rollbackErr, auditErr)
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

func (service *Service) ReplacePluginGitHubToken(ctx context.Context, pluginID, token string) error {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return invalid("plugin_id", err.Error())
	}
	normalized, err := normalizedRequired("github_token", token, 4096)
	if err != nil {
		return err
	}
	if service.secrets == nil || !service.secrets.Available() {
		return secretbox.ErrUnavailable
	}
	ciphertext, err := service.secrets.Encrypt(
		store.PluginInstallationSecretOwnerType, pluginID, store.PluginInstallationGitHubToken, []byte(normalized),
	)
	if err != nil {
		return fmt.Errorf("encrypt GitHub token: %w", err)
	}
	return service.store.ReplacePluginGitHubToken(ctx, pluginID, ciphertext, service.currentTime())
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
	canonicalRepository, token, existingByRepository, err := service.pluginRepositoryAccess(ctx, input.Repository, input.GitHubToken)
	if err != nil {
		return githubrelease.Release{}, "", nil, err
	}
	release, err := service.pluginReleases.Inspect(ctx, canonicalRepository, input.Version, token)
	if err != nil {
		return githubrelease.Release{}, "", nil, translateGitHubReleaseError(err)
	}
	for _, permission := range release.Manifest.Permissions {
		if permission.Name != centerpluginv1.PermissionEventsRead && permission.Name != centerpluginv1.PermissionEventsWrite &&
			permission.Name != centerpluginv1.PermissionNodeConfigure && permission.Name != centerpluginv1.PermissionNodesRead &&
			permission.Name != centerpluginv1.PermissionServicesWrite {
			return githubrelease.Release{}, "", nil, invalid("repository", "release requests an unsupported permission: "+permission.Name)
		}
		if permission.Name == centerpluginv1.PermissionEventsRead && release.Manifest.Kind != manifest.KindFeature {
			return githubrelease.Release{}, "", nil, invalid("repository", "only feature plugins can request core.events.read")
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

func (service *Service) pluginRepositoryAccess(ctx context.Context, rawRepository, suppliedToken string) (string, string, *store.PluginInstallation, error) {
	repository, err := githubrelease.ParseRepository(rawRepository)
	if err != nil {
		return "", "", nil, invalid("repository", err.Error())
	}
	canonicalRepository := repository.URL()
	var existing *store.PluginInstallation
	if value, err := service.store.PluginInstallationByRepository(ctx, canonicalRepository); err == nil {
		existing = &value
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", "", nil, err
	}
	token := suppliedToken
	if token == "" && existing != nil {
		token, err = service.decryptPluginGitHubToken(ctx, existing.PluginID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", "", nil, err
		}
	}
	return canonicalRepository, token, existing, nil
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

func (service *Service) recordPluginReleaseFailure(version store.PluginVersion, repository string,
	existing *store.PluginInstallation, stage string, rollbackSucceeded *bool,
) error {
	now := service.currentTime()
	auditContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	action := "plugin.install"
	metadata := map[string]any{"repository": repository, "version": version.Version, "stage": stage}
	if existing != nil {
		action = "plugin.upgrade"
		metadata["previous_version"] = existing.ActiveVersion
	}
	attemptErr := service.store.AppendAudit(auditContext, store.AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: action,
		TargetType: "plugin_installation", TargetID: version.PluginID, Outcome: "failure", Metadata: metadata,
	})
	if rollbackSucceeded == nil || existing == nil {
		return attemptErr
	}
	outcome := "failure"
	if *rollbackSucceeded {
		outcome = "success"
	}
	rollbackErr := service.store.AppendAudit(auditContext, store.AuditEntry{
		OccurredAt: now, ActorType: "system", Action: "plugin.rollback",
		TargetType: "plugin_installation", TargetID: version.PluginID, Outcome: outcome,
		Metadata: map[string]any{
			"failed_version": version.Version, "restored_version": existing.ActiveVersion, "trigger": stage + "_failure",
		},
	})
	return errors.Join(attemptErr, rollbackErr)
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
		return invalidWithCause("release", "could not be read or validated", fmt.Errorf("inspect GitHub release: %w", err))
	}
}
