package management

import (
	"context"
	"errors"
	"fmt"

	"github.com/Relayward/relayward/internal/agentrelease"
	"github.com/Relayward/relayward/internal/store"
)

const (
	AgentVersionAvailable = "available"
	AgentVersionCurrent   = "current"
	AgentVersionAhead     = "ahead"
	AgentVersionUnknown   = "unknown"
)

type agentReleaseProvider interface {
	Latest(context.Context) (agentrelease.Release, error)
}

type AgentUpdateAvailability struct {
	CurrentVersion string
	LatestRelease  agentrelease.Release
	Relation       string
}

func (service *Service) ConfigureAgentReleases(provider agentReleaseProvider) error {
	if provider == nil {
		return errors.New("Agent release provider is required")
	}
	service.agentReleases = provider
	return nil
}

func (service *Service) AgentUpdateAvailability(ctx context.Context, nodeID string) (AgentUpdateAvailability, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return AgentUpdateAvailability{}, err
	}
	node, err := service.store.NodeByID(ctx, nodeID)
	if err != nil {
		return AgentUpdateAvailability{}, err
	}
	release, err := service.latestAgentRelease(ctx)
	if err != nil {
		return AgentUpdateAvailability{}, err
	}
	compared, err := agentrelease.Compare(node.AgentVersion, release.Version)
	relation := AgentVersionUnknown
	if err == nil {
		switch {
		case compared < 0:
			relation = AgentVersionAvailable
		case compared == 0:
			relation = AgentVersionCurrent
		default:
			relation = AgentVersionAhead
		}
	}
	return AgentUpdateAvailability{
		CurrentVersion: node.AgentVersion, LatestRelease: release, Relation: relation,
	}, nil
}

func (service *Service) RequestLatestAgentUpdate(ctx context.Context, nodeID string) (store.AgentCommand, error) {
	node, err := service.agentUpdateNode(ctx, nodeID)
	if err != nil {
		return store.AgentCommand{}, err
	}
	release, err := service.latestAgentRelease(ctx)
	if err != nil {
		return store.AgentCommand{}, err
	}
	compared, err := agentrelease.Compare(node.AgentVersion, release.Version)
	if err != nil {
		return store.AgentCommand{}, invalid("node_id", "Agent does not report a semantic version")
	}
	switch {
	case compared == 0:
		return store.AgentCommand{}, invalid("version", "latest release is already active on this node")
	case compared > 0:
		return store.AgentCommand{}, invalid("version", "node version is newer than the latest release")
	}
	return service.queueAgentUpdate(ctx, node, release.Version)
}

func (service *Service) latestAgentRelease(ctx context.Context) (agentrelease.Release, error) {
	if service.agentReleases == nil {
		return agentrelease.Release{}, ErrUpstreamUnavailable
	}
	release, err := service.agentReleases.Latest(ctx)
	if err != nil {
		return agentrelease.Release{}, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	return release, nil
}
