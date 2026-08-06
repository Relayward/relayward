package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const pluginReconcileCommandLifetime = 30 * time.Minute

var githubRepositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type NodePluginInput struct {
	DesiredState  string
	Version       string
	Configuration json.RawMessage
}

func (service *Service) ListNodePluginInstances(ctx context.Context) ([]store.NodePluginInstance, error) {
	if err := service.store.ExpireNodePluginCommands(ctx, service.currentTime()); err != nil {
		return nil, err
	}
	return service.store.ListNodePluginInstances(ctx)
}

func (service *Service) RecordNodePluginStatus(ctx context.Context, nodeID string, status agentv1.PluginStatusEvent, observedAt, receivedAt time.Time) error {
	return service.store.RecordNodePluginStatus(ctx, nodeID, status, observedAt, receivedAt)
}

func (service *Service) NodePluginInstance(ctx context.Context, nodeID, pluginID string) (store.NodePluginInstance, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return store.NodePluginInstance{}, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return store.NodePluginInstance{}, invalid("plugin_id", err.Error())
	}
	if err := service.store.ExpireNodePluginCommands(ctx, service.currentTime()); err != nil {
		return store.NodePluginInstance{}, err
	}
	return service.store.NodePluginInstanceByID(ctx, nodeID, pluginID)
}

func (service *Service) ReconcileNodePlugin(ctx context.Context, nodeID, pluginID string, input NodePluginInput) (store.NodePluginInstance, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return store.NodePluginInstance{}, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return store.NodePluginInstance{}, invalid("plugin_id", err.Error())
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	return service.reconcileNodePluginLocked(ctx, nodeID, pluginID, input, nil, false)
}

func (service *Service) NodePluginConfiguration(ctx context.Context, nodeID, pluginID string) (store.NodePluginInstance, json.RawMessage, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return store.NodePluginInstance{}, nil, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return store.NodePluginInstance{}, nil, invalid("plugin_id", err.Error())
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	instance, err := service.store.NodePluginInstanceByID(ctx, nodeID, pluginID)
	if err != nil {
		return store.NodePluginInstance{}, nil, err
	}
	if instance.DesiredState == agentv1.PluginStateAbsent || instance.DesiredConfigurationSHA256 == "" {
		return store.NodePluginInstance{}, nil, store.ErrNotFound
	}
	configuration, err := service.decryptNodePluginConfiguration(ctx, nodeID, pluginID)
	if err != nil {
		return store.NodePluginInstance{}, nil, err
	}
	digest, err := agentv1.PluginConfigurationDigest(configuration)
	if err != nil {
		return store.NodePluginInstance{}, nil, fmt.Errorf("validate stored plugin configuration: %w", err)
	}
	if digest != instance.DesiredConfigurationSHA256 {
		return store.NodePluginInstance{}, nil, errors.New("stored plugin configuration does not match its desired digest")
	}
	return instance, configuration, nil
}

func (service *Service) ConfigureNodePlugin(ctx context.Context, nodeID, pluginID, version string,
	expectedGeneration uint64, configuration json.RawMessage,
) (store.NodePluginInstance, error) {
	if expectedGeneration >= math.MaxInt64 {
		return store.NodePluginInstance{}, invalid("expected_generation", "must be less than the maximum signed 64-bit integer")
	}
	if err := validateID("node_id", nodeID); err != nil {
		return store.NodePluginInstance{}, err
	}
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return store.NodePluginInstance{}, invalid("plugin_id", err.Error())
	}
	service.pluginMu.Lock()
	defer service.pluginMu.Unlock()
	result, err := service.reconcileNodePluginLocked(ctx, nodeID, pluginID, NodePluginInput{
		DesiredState:  agentv1.PluginStateRunning,
		Version:       version,
		Configuration: append(json.RawMessage(nil), configuration...),
	}, &expectedGeneration, true)
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		return store.NodePluginInstance{}, fmt.Errorf("%w: %v", store.ErrStateConflict, err)
	}
	return result, err
}

func (service *Service) reconcileNodePluginLocked(ctx context.Context, nodeID, pluginID string, input NodePluginInput,
	expectedGeneration *uint64, pluginActor bool,
) (store.NodePluginInstance, error) {

	node, err := service.store.NodeByID(ctx, nodeID)
	if err != nil {
		return store.NodePluginInstance{}, err
	}
	switch {
	case !node.Enabled:
		return store.NodePluginInstance{}, invalid("node_id", "must be enabled before configuring plugins")
	case node.RegisteredAt == nil:
		return store.NodePluginInstance{}, invalid("node_id", "must have a registered Agent")
	case !containsCapability(node.Capabilities, agentv1.CapabilityControlCommands):
		return store.NodePluginInstance{}, invalid("node_id", "Agent does not support durable commands")
	case !containsCapability(node.Capabilities, agentv1.CapabilityPluginSupervision):
		return store.NodePluginInstance{}, invalid("node_id", "Agent does not support plugin supervision")
	}
	installation, err := service.store.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return store.NodePluginInstance{}, err
	}
	if installation.Kind != string(manifest.KindRuntime) || installation.Manifest.Kind != manifest.KindRuntime {
		return store.NodePluginInstance{}, invalid("plugin_id", "must identify an installed runtime plugin")
	}
	if installation.State != "active" || installation.ActiveVersion == "" {
		return store.NodePluginInstance{}, invalid("plugin_id", "must be active before configuring nodes")
	}
	if installation.Manifest.ID != pluginID || installation.Manifest.Version != installation.ActiveVersion {
		return store.NodePluginInstance{}, errors.New("installed plugin manifest does not match its active version")
	}

	reconcile := agentv1.PluginReconcileCommand{
		PluginID: pluginID, DesiredState: input.DesiredState, Version: strings.TrimSpace(input.Version),
		Configuration: input.Configuration,
	}
	var current *store.NodePluginInstance
	if value, err := service.store.NodePluginInstanceByID(ctx, nodeID, pluginID); err == nil {
		current = &value
	} else if errors.Is(err, store.ErrNotFound) {
	} else {
		return store.NodePluginInstance{}, err
	}
	currentGeneration := uint64(0)
	if current != nil {
		currentGeneration = current.Generation
	}
	if expectedGeneration != nil && *expectedGeneration != currentGeneration {
		return store.NodePluginInstance{}, store.ErrGenerationConflict
	}
	if currentGeneration >= math.MaxInt64 {
		return store.NodePluginInstance{}, store.ErrGenerationConflict
	}
	reconcile.Generation = currentGeneration + 1
	if input.DesiredState != agentv1.PluginStateAbsent {
		if reconcile.Version != installation.ActiveVersion {
			return store.NodePluginInstance{}, invalid("version", "must match the installed active plugin version")
		}
		artifact, ok := nodeArtifact(installation.Manifest)
		if !ok {
			return store.NodePluginInstance{}, invalid("plugin_id", "does not provide a node artifact")
		}
		downloadURL, err := service.nodePluginArtifactURL(ctx, installation, artifact)
		if err != nil {
			return store.NodePluginInstance{}, fmt.Errorf("resolve node plugin artifact: %w", err)
		}
		reconcile.Artifact = &agentv1.PluginArtifact{
			DownloadURL: downloadURL, Size: artifact.Size, SHA256: artifact.SHA256,
		}
		if len(reconcile.Configuration) == 0 && current != nil {
			if current.DesiredConfigurationSHA256 == "" {
				return store.NodePluginInstance{}, invalid("configuration", "is required when no stored configuration exists")
			}
			configuration, err := service.decryptNodePluginConfiguration(ctx, nodeID, pluginID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return store.NodePluginInstance{}, invalid("configuration", "is required when no stored configuration exists")
				}
				return store.NodePluginInstance{}, err
			}
			reconcile.Configuration = configuration
		}
	}
	if err := agentv1.ValidatePluginReconcileCommand(reconcile); err != nil {
		return store.NodePluginInstance{}, invalid("body", err.Error())
	}
	configurationSHA256 := ""
	if reconcile.DesiredState != agentv1.PluginStateAbsent {
		configurationSHA256, err = agentv1.PluginConfigurationDigest(reconcile.Configuration)
		if err != nil {
			return store.NodePluginInstance{}, invalid("configuration", err.Error())
		}
	}
	now := service.currentTime()
	command, err := agentv1.NewPluginReconcileCommand(reconcile, now, now.Add(pluginReconcileCommandLifetime))
	if err != nil {
		return store.NodePluginInstance{}, fmt.Errorf("create plugin reconcile command: %w", err)
	}
	commandID := uuid.NewString()
	commandPlaintext, err := json.Marshal(command)
	if err != nil {
		return store.NodePluginInstance{}, fmt.Errorf("encode plugin reconcile command: %w", err)
	}
	if service.secrets == nil || !service.secrets.Available() {
		return store.NodePluginInstance{}, secretbox.ErrUnavailable
	}
	commandCiphertext, err := service.secrets.Encrypt(
		store.AgentCommandSecretOwnerType, commandID, store.AgentCommandRequestSecret, commandPlaintext,
	)
	if err != nil {
		return store.NodePluginInstance{}, fmt.Errorf("encrypt plugin reconcile command: %w", err)
	}
	var configurationCiphertext []byte
	if reconcile.DesiredState != agentv1.PluginStateAbsent {
		ownerID := store.NodePluginSecretOwnerID(nodeID, pluginID)
		configurationCiphertext, err = service.secrets.Encrypt(
			store.NodePluginSecretOwnerType, ownerID, store.NodePluginConfigurationSecret, reconcile.Configuration,
		)
		if err != nil {
			return store.NodePluginInstance{}, fmt.Errorf("encrypt desired plugin configuration: %w", err)
		}
	}
	desired := store.NodePluginDesired{
		NodeID: nodeID, PluginID: pluginID, Generation: reconcile.Generation,
		DesiredState: reconcile.DesiredState, DesiredVersion: reconcile.Version,
		DesiredConfigurationSHA256: configurationSHA256,
	}
	if reconcile.Artifact != nil {
		desired.ArtifactSize = reconcile.Artifact.Size
		desired.ArtifactSHA256 = reconcile.Artifact.SHA256
	}
	if pluginActor {
		return service.store.ApplyNodePluginDesiredByPlugin(
			ctx, desired, configurationCiphertext, commandID, command, commandCiphertext, now,
		)
	}
	return service.store.ApplyNodePluginDesired(ctx, desired, configurationCiphertext, commandID, command, commandCiphertext, now)
}

func (service *Service) nodePluginArtifactURL(ctx context.Context, installation store.PluginInstallation, artifact manifest.Artifact) (string, error) {
	ciphertext, err := service.store.Secret(
		ctx, store.PluginInstallationSecretOwnerType, installation.PluginID, store.PluginInstallationGitHubToken,
	)
	if errors.Is(err, store.ErrNotFound) {
		return publicReleaseAssetURL(installation.Repository, installation.ActiveVersion, artifact.File)
	}
	if err != nil {
		return "", err
	}
	if service.pluginReleases == nil || service.secrets == nil || !service.secrets.Available() {
		return "", secretbox.ErrUnavailable
	}
	plaintext, err := service.secrets.Decrypt(
		store.PluginInstallationSecretOwnerType, installation.PluginID, store.PluginInstallationGitHubToken, ciphertext,
	)
	if err != nil {
		return "", fmt.Errorf("decrypt GitHub token: %w", err)
	}
	version, err := service.store.PluginVersionByID(ctx, installation.PluginID, installation.ActiveVersion)
	if err != nil {
		return "", err
	}
	if version.NodeAssetID == nil {
		return "", errors.New("installed plugin version does not contain a node asset ID")
	}
	repository, err := githubrelease.ParseRepository(installation.Repository)
	if err != nil {
		return "", err
	}
	downloadURL, err := service.pluginReleases.ResolveAssetURL(ctx, repository, *version.NodeAssetID, string(plaintext))
	if err != nil {
		return "", translateGitHubReleaseError(err)
	}
	return downloadURL, nil
}

func (service *Service) decryptNodePluginConfiguration(ctx context.Context, nodeID, pluginID string) (json.RawMessage, error) {
	if service.secrets == nil || !service.secrets.Available() {
		return nil, secretbox.ErrUnavailable
	}
	ownerID := store.NodePluginSecretOwnerID(nodeID, pluginID)
	ciphertext, err := service.store.Secret(ctx, store.NodePluginSecretOwnerType, ownerID, store.NodePluginConfigurationSecret)
	if err != nil {
		return nil, err
	}
	plaintext, err := service.secrets.Decrypt(
		store.NodePluginSecretOwnerType, ownerID, store.NodePluginConfigurationSecret, ciphertext,
	)
	if err != nil {
		return nil, fmt.Errorf("decrypt desired plugin configuration: %w", err)
	}
	return json.RawMessage(plaintext), nil
}

func nodeArtifact(value manifest.Manifest) (manifest.Artifact, bool) {
	for _, artifact := range value.Artifacts {
		if artifact.Role == manifest.ArtifactNode {
			return artifact, true
		}
	}
	return manifest.Artifact{}, false
}

func publicReleaseAssetURL(repository, version, file string) (string, error) {
	parsed, err := url.Parse(repository)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("plugin repository must be an HTTPS github.com repository URL")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return "", errors.New("plugin repository must contain exactly an owner and repository")
	}
	owner, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", errors.New("plugin repository owner is invalid")
	}
	repositoryName, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", errors.New("plugin repository name is invalid")
	}
	repositoryName = strings.TrimSuffix(repositoryName, ".git")
	if !githubRepositoryPartPattern.MatchString(owner) || !githubRepositoryPartPattern.MatchString(repositoryName) ||
		owner == "." || owner == ".." || repositoryName == "." || repositoryName == ".." {
		return "", errors.New("plugin repository owner or name is invalid")
	}
	result := &url.URL{
		Scheme: "https", Host: "github.com",
		Path: "/" + owner + "/" + repositoryName + "/releases/download/v" + version + "/" + file,
	}
	return result.String(), nil
}
