package management

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"

	"github.com/Relayward/relayward/internal/agentrelease"
	"github.com/Relayward/relayward/internal/store"
)

func TestAgentUpdateAvailabilityAndLatestRequest(t *testing.T) {
	service := newTestService(t)
	provider := &agentReleaseStub{release: agentrelease.Release{
		Version: "0.2.0", Tag: "v0.2.0", PublishedAt: time.Now().UTC(), CheckedAt: time.Now().UTC(),
	}}
	if err := service.ConfigureAgentReleases(provider); err != nil {
		t.Fatalf("ConfigureAgentReleases() error = %v", err)
	}
	node := registerManagedAgent(t, service, "Updatable latest", []string{
		agentv1.CapabilityAgentSelfUpdate, agentv1.CapabilityControlCommands,
	})

	availability, err := service.AgentUpdateAvailability(context.Background(), node.ID)
	if err != nil || availability.CurrentVersion != "0.1.0" || availability.LatestRelease.Version != "0.2.0" ||
		availability.Relation != AgentVersionAvailable {
		t.Fatalf("AgentUpdateAvailability() = %+v, %v", availability, err)
	}
	command, err := service.RequestLatestAgentUpdate(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("RequestLatestAgentUpdate() error = %v", err)
	}
	payload, err := agentv1.DecodeAgentUpdateCommand(command.Request)
	if err != nil || payload.Version != "0.2.0" || command.Status != store.AgentCommandPending {
		t.Fatalf("latest Agent command = %+v, payload = %+v, %v", command, payload, err)
	}
}

func TestLatestAgentUpdateRejectsUnknownCurrentAndDowngrade(t *testing.T) {
	service := newTestService(t)
	provider := &agentReleaseStub{release: agentrelease.Release{Version: "0.1.0", Tag: "v0.1.0"}}
	_ = service.ConfigureAgentReleases(provider)
	node := registerManagedAgent(t, service, "Current", []string{
		agentv1.CapabilityAgentSelfUpdate, agentv1.CapabilityControlCommands,
	})
	if _, err := service.RequestLatestAgentUpdate(context.Background(), node.ID); fieldName(err) != "version" {
		t.Fatalf("RequestLatestAgentUpdate() current error = %v", err)
	}

	provider.release.Version = "0.0.9"
	provider.release.Tag = "v0.0.9"
	if _, err := service.RequestLatestAgentUpdate(context.Background(), node.ID); fieldName(err) != "version" {
		t.Fatalf("RequestLatestAgentUpdate() downgrade error = %v", err)
	}

	stored, err := service.store.NodeByID(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if err := service.store.RecordAgentHeartbeat(context.Background(), stored.ID, stored.CredentialHash, "dev", time.Now().UTC()); err != nil {
		t.Fatalf("RecordAgentHeartbeat() error = %v", err)
	}
	if _, err := service.RequestLatestAgentUpdate(context.Background(), node.ID); fieldName(err) != "node_id" {
		t.Fatalf("RequestLatestAgentUpdate() development version error = %v", err)
	}
}

func TestAgentReleaseFailureIsExplicit(t *testing.T) {
	service := newTestService(t)
	_ = service.ConfigureAgentReleases(&agentReleaseStub{err: errors.New("offline")})
	node, err := service.CreateNode(context.Background(), NodeInput{Name: "No release", Enabled: true})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := service.AgentUpdateAvailability(context.Background(), node.ID); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("AgentUpdateAvailability() error = %v", err)
	}
}

type agentReleaseStub struct {
	release agentrelease.Release
	err     error
}

func (stub *agentReleaseStub) Latest(context.Context) (agentrelease.Release, error) {
	return stub.release, stub.err
}
