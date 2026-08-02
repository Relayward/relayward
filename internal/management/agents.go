package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/contract"
	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const agentUpdateCommandLifetime = 30 * time.Minute

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
	command, err := service.store.NextAgentCommand(ctx, nodeID, now)
	if err != nil {
		return store.AgentCommand{}, err
	}
	if err := service.decryptAgentCommand(ctx, &command); err != nil {
		return store.AgentCommand{}, err
	}
	return command, nil
}

func (service *Service) MarkAgentCommandSent(ctx context.Context, commandID, nodeID string, sentAt time.Time) error {
	return service.store.MarkAgentCommandSent(ctx, commandID, nodeID, sentAt)
}

func (service *Service) CompleteAgentCommand(ctx context.Context, nodeID string, credentialHash []byte, result agentv1.CommandResult, receivedAt time.Time) error {
	command, err := service.store.AgentCommandByID(ctx, result.CommandID)
	if err != nil || command.NodeID != nodeID {
		return store.ErrNotFound
	}
	if !command.RequestEncrypted || command.Status != store.AgentCommandPending {
		return service.store.CompleteAgentCommand(ctx, nodeID, credentialHash, result, receivedAt)
	}
	if err := service.decryptAgentCommand(ctx, &command); err != nil {
		if err == store.ErrNotFound {
			return service.store.CompleteAgentCommand(ctx, nodeID, credentialHash, result, receivedAt)
		}
		return err
	}
	return service.store.CompleteEncryptedAgentCommand(ctx, nodeID, credentialHash, result, command.Request, receivedAt)
}

func (service *Service) RequestAgentUpdate(ctx context.Context, nodeID, version string) (store.AgentCommand, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return store.AgentCommand{}, err
	}
	if err := contract.ValidateSemanticVersion(version); err != nil {
		return store.AgentCommand{}, invalid("version", err.Error())
	}
	node, err := service.store.NodeByID(ctx, nodeID)
	if err != nil {
		return store.AgentCommand{}, err
	}
	switch {
	case !node.Enabled:
		return store.AgentCommand{}, invalid("node_id", "must be enabled before updating its Agent")
	case node.RegisteredAt == nil:
		return store.AgentCommand{}, invalid("node_id", "must have a registered Agent")
	case !containsCapability(node.Capabilities, agentv1.CapabilityControlCommands):
		return store.AgentCommand{}, invalid("node_id", "Agent does not support durable commands")
	case !containsCapability(node.Capabilities, agentv1.CapabilityAgentSelfUpdate):
		return store.AgentCommand{}, invalid("node_id", "Agent does not support self-update")
	case node.AgentVersion == version:
		return store.AgentCommand{}, invalid("version", "is already active on this node")
	}
	now := service.currentTime()
	command, err := agentv1.NewAgentUpdateCommand(version, now, now.Add(agentUpdateCommandLifetime))
	if err != nil {
		return store.AgentCommand{}, fmt.Errorf("create Agent update command: %w", err)
	}
	return service.store.CreateAgentCommand(ctx, uuid.NewString(), nodeID, command, now)
}

func (service *Service) LatestAgentUpdate(ctx context.Context, nodeID string) (store.AgentCommand, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return store.AgentCommand{}, err
	}
	if _, err := service.store.NodeByID(ctx, nodeID); err != nil {
		return store.AgentCommand{}, err
	}
	return service.store.LatestAgentCommandByKind(ctx, nodeID, agentv1.CommandAgentUpdate, service.currentTime())
}

func containsCapability(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (service *Service) decryptAgentCommand(ctx context.Context, command *store.AgentCommand) error {
	if !command.RequestEncrypted {
		return nil
	}
	if service.secrets == nil || !service.secrets.Available() {
		return fmt.Errorf("decrypt Agent command: %w", secretbox.ErrUnavailable)
	}
	ciphertext, err := service.store.Secret(ctx, store.AgentCommandSecretOwnerType, command.ID, store.AgentCommandRequestSecret)
	if err != nil {
		return err
	}
	plaintext, err := service.secrets.Decrypt(store.AgentCommandSecretOwnerType, command.ID, store.AgentCommandRequestSecret, ciphertext)
	if err != nil {
		return fmt.Errorf("decrypt Agent command: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	var request agentv1.Command
	if err := decoder.Decode(&request); err != nil {
		return fmt.Errorf("decode encrypted Agent command: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("decode encrypted Agent command: trailing JSON value")
	}
	digest, err := agentv1.CommandDigest(request)
	if err != nil {
		return fmt.Errorf("validate encrypted Agent command: %w", err)
	}
	if digest != command.RequestSHA256 || request.Kind != command.Kind {
		return fmt.Errorf("validate encrypted Agent command: digest mismatch")
	}
	command.Request = request
	return nil
}
