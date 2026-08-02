package management

import (
	"context"
	"fmt"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/store"
)

type RegisteredAgent struct {
	Node       store.Node
	Credential string
}

func (service *Service) RegisterAgent(ctx context.Context, request agentv1.RegisterRequest) (RegisteredAgent, error) {
	if err := agentv1.ValidateRegisterRequest(request); err != nil {
		return RegisteredAgent{}, invalid("body", err.Error())
	}
	value, err := auth.NewToken(32)
	if err != nil {
		return RegisteredAgent{}, fmt.Errorf("generate node credential: %w", err)
	}
	credential := "rwc_" + value
	now := service.currentTime()
	node, err := service.store.RegisterAgent(ctx, store.AgentRegistration{
		TokenHash: auth.TokenHash(request.Token), CredentialHash: auth.TokenHash(credential),
		AgentVersion: request.AgentVersion, Hostname: request.Hostname, OS: request.OS, Arch: request.Arch,
		Capabilities: request.Capabilities,
	}, now)
	if err != nil {
		return RegisteredAgent{}, err
	}
	return RegisteredAgent{Node: node, Credential: credential}, nil
}

func (service *Service) AuthenticateAgent(ctx context.Context, nodeID, credential string) (store.Node, error) {
	if err := agentv1.ValidateNodeID(nodeID); err != nil {
		return store.Node{}, store.ErrNotFound
	}
	if err := agentv1.ValidateCredential(credential); err != nil {
		return store.Node{}, store.ErrNotFound
	}
	return service.store.AuthenticateAgent(ctx, nodeID, auth.TokenHash(credential))
}

func (service *Service) RecordAgentHello(ctx context.Context, nodeID string, credentialHash []byte, hello agentv1.AgentHello, receivedAt time.Time) error {
	if err := agentv1.ValidateNodeID(nodeID); err != nil {
		return store.ErrNotFound
	}
	if err := agentv1.ValidateAgentHello(hello); err != nil || hello.NodeID != nodeID {
		return store.ErrNotFound
	}
	return service.store.RecordAgentHello(ctx, nodeID, credentialHash, hello.AgentVersion, hello.Capabilities, receivedAt)
}

func (service *Service) RecordAgentHeartbeat(ctx context.Context, nodeID string, credentialHash []byte, heartbeat agentv1.Heartbeat, receivedAt time.Time) error {
	if err := agentv1.ValidateNodeID(nodeID); err != nil {
		return store.ErrNotFound
	}
	if err := agentv1.ValidateHeartbeat(heartbeat); err != nil {
		return store.ErrNotFound
	}
	return service.store.RecordAgentHeartbeat(ctx, nodeID, credentialHash, heartbeat.AgentVersion, receivedAt)
}

func (service *Service) NextAgentCommand(ctx context.Context, nodeID string, now time.Time) (store.AgentCommand, error) {
	return service.store.NextAgentCommand(ctx, nodeID, now)
}

func (service *Service) MarkAgentCommandSent(ctx context.Context, commandID, nodeID string, sentAt time.Time) error {
	return service.store.MarkAgentCommandSent(ctx, commandID, nodeID, sentAt)
}

func (service *Service) CompleteAgentCommand(ctx context.Context, nodeID string, credentialHash []byte, result agentv1.CommandResult, receivedAt time.Time) error {
	return service.store.CompleteAgentCommand(ctx, nodeID, credentialHash, result, receivedAt)
}
