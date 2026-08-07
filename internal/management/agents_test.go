package management

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"

	"github.com/Relayward/relayward/internal/store"
)

func TestRequestAgentUpdateValidationAndLifecycle(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 2, 11, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	unregistered, err := service.CreateNode(ctx, NodeInput{Name: "Unregistered", Enabled: true})
	if err != nil {
		t.Fatalf("CreateNode() unregistered error = %v", err)
	}
	if _, err := service.RequestAgentUpdate(ctx, unregistered.ID, "0.2.0"); fieldName(err) != "node_id" {
		t.Fatalf("RequestAgentUpdate() unregistered error = %v", err)
	}
	if _, err := service.RequestAgentUpdate(ctx, unregistered.ID, "v0.2.0"); fieldName(err) != "version" {
		t.Fatalf("RequestAgentUpdate() invalid version error = %v", err)
	}

	commandsOnly := registerManagedAgent(t, service, "Commands only", []string{agentv1.CapabilityControlCommands})
	if _, err := service.RequestAgentUpdate(ctx, commandsOnly.ID, "0.2.0"); fieldName(err) != "node_id" {
		t.Fatalf("RequestAgentUpdate() missing self-update capability error = %v", err)
	}
	selfUpdateOnly := registerManagedAgent(t, service, "Update only", []string{agentv1.CapabilityAgentSelfUpdate})
	if _, err := service.RequestAgentUpdate(ctx, selfUpdateOnly.ID, "0.2.0"); fieldName(err) != "node_id" {
		t.Fatalf("RequestAgentUpdate() missing command capability error = %v", err)
	}

	node := registerManagedAgent(t, service, "Updatable", []string{
		agentv1.CapabilityAgentSelfUpdate,
		agentv1.CapabilityControlCommands,
	})
	if _, err := service.RequestAgentUpdate(ctx, node.ID, node.AgentVersion); fieldName(err) != "version" {
		t.Fatalf("RequestAgentUpdate() active version error = %v", err)
	}
	if _, err := service.RequestAgentUpdate(ctx, node.ID, "0.0.9"); fieldName(err) != "version" {
		t.Fatalf("RequestAgentUpdate() downgrade error = %v", err)
	}
	created, err := service.RequestAgentUpdate(ctx, node.ID, "0.2.0")
	if err != nil {
		t.Fatalf("RequestAgentUpdate() error = %v", err)
	}
	payload, err := agentv1.DecodeAgentUpdateCommand(created.Request)
	if err != nil || payload.Version != "0.2.0" || created.Status != store.AgentCommandPending ||
		!created.ExpiresAt.Equal(now.Add(agentUpdateCommandLifetime)) {
		t.Fatalf("created Agent update = %+v, payload = %+v, %v", created, payload, err)
	}
	latest, err := service.LatestAgentUpdate(ctx, node.ID)
	if err != nil || latest.ID != created.ID {
		t.Fatalf("LatestAgentUpdate() = %+v, %v", latest, err)
	}
	if _, err := service.RequestAgentUpdate(ctx, node.ID, "0.3.0"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("RequestAgentUpdate() second pending error = %v", err)
	}

	now = now.Add(agentUpdateCommandLifetime)
	replacement, err := service.RequestAgentUpdate(ctx, node.ID, "0.3.0")
	if err != nil || replacement.ID == created.ID {
		t.Fatalf("RequestAgentUpdate() after expiry = %+v, %v", replacement, err)
	}
	expired, err := service.store.AgentCommandByID(ctx, created.ID)
	if err != nil || expired.Status != store.AgentCommandExpired {
		t.Fatalf("expired Agent update = %+v, %v", expired, err)
	}

	registered, err := service.Node(ctx, node.ID)
	if err != nil {
		t.Fatalf("Node() error = %v", err)
	}
	if _, err := service.UpdateNode(ctx, node.ID, NodeInput{Name: registered.Name, Enabled: false}); err != nil {
		t.Fatalf("UpdateNode() disable error = %v", err)
	}
	if _, err := service.RequestAgentUpdate(ctx, node.ID, "0.4.0"); fieldName(err) != "node_id" {
		t.Fatalf("RequestAgentUpdate() disabled error = %v", err)
	}
}

func TestRevokeNodeCredential(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	node := registerManagedAgent(t, service, "Revoked", []string{agentv1.CapabilityControlHeartbeat})

	revoked, err := service.RevokeNodeCredential(ctx, node.ID)
	if err != nil {
		t.Fatalf("RevokeNodeCredential() error = %v", err)
	}
	if revoked.RegisteredAt != nil || revoked.CredentialHash != nil || revoked.AgentVersion != "" || len(revoked.Capabilities) != 0 {
		t.Fatalf("revoked node = %+v", revoked)
	}
	if _, err := service.RevokeNodeCredential(ctx, node.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second RevokeNodeCredential() error = %v", err)
	}
	if _, err := service.RevokeNodeCredential(ctx, "not-a-uuid"); fieldName(err) != "node_id" {
		t.Fatalf("RevokeNodeCredential() invalid ID error = %v", err)
	}
}

func registerManagedAgent(t *testing.T, service *Service, name string, capabilities []string) store.Node {
	t.Helper()
	ctx := context.Background()
	node, err := service.CreateNode(ctx, NodeInput{Name: name, Enabled: true})
	if err != nil {
		t.Fatalf("CreateNode(%q) error = %v", name, err)
	}
	token, err := service.CreateRegistrationToken(ctx, node.ID)
	if err != nil {
		t.Fatalf("CreateRegistrationToken(%q) error = %v", name, err)
	}
	registered, err := service.RegisterAgent(ctx, agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: token.Token, AgentVersion: "0.1.0",
		Hostname: "edge", OS: "linux", Arch: "amd64", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("RegisterAgent(%q) error = %v", name, err)
	}
	return registered.Node
}
