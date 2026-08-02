package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type AgentRegistration struct {
	TokenHash      []byte
	CredentialHash []byte
	AgentVersion   string
	Hostname       string
	OS             string
	Arch           string
	Capabilities   []string
}

func (store *Store) RegisterAgent(ctx context.Context, registration AgentRegistration, now time.Time) (Node, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("begin Agent registration: %w", err)
	}
	defer tx.Rollback()

	var nodeID string
	err = tx.QueryRowContext(ctx, `
UPDATE node_registration_tokens SET used_at = ?
WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?
RETURNING node_id`, unixTime(now), registration.TokenHash, unixTime(now)).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("consume Agent registration token: %w", err)
	}
	var hadCredential bool
	var registrationCount uint64
	if err := tx.QueryRowContext(ctx, `
SELECT credential_hash IS NOT NULL, registration_count FROM nodes WHERE id = ?`, nodeID).Scan(&hadCredential, &registrationCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNotFound
		}
		return Node{}, fmt.Errorf("read Agent registration state: %w", err)
	}
	capabilities, err := json.Marshal(registration.Capabilities)
	if err != nil {
		return Node{}, fmt.Errorf("encode Agent capabilities: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE nodes SET credential_hash = ?, registered_at = ?, last_seen_at = NULL, registration_count = registration_count + 1,
    hostname = ?, agent_version = ?, agent_os = ?, agent_arch = ?, agent_capabilities_json = ?, updated_at = ?
WHERE id = ? AND enabled = 1`, registration.CredentialHash, unixTime(now), registration.Hostname,
		registration.AgentVersion, registration.OS, registration.Arch, string(capabilities), unixTime(now), nodeID)
	if err != nil {
		return Node{}, fmt.Errorf("register Agent: %w", err)
	}
	registered, err := result.RowsAffected()
	if err != nil {
		return Node{}, fmt.Errorf("read Agent registration result: %w", err)
	}
	if registered != 1 {
		return Node{}, ErrNotFound
	}
	action := "node.register"
	if hadCredential {
		action = "node.credential.rotate"
	} else if registrationCount > 0 {
		action = "node.reregister"
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "agent", ActorID: nodeID, Action: action,
		TargetType: "node", TargetID: nodeID, Outcome: "success",
		Metadata: map[string]any{"agent_version": registration.AgentVersion, "os": registration.OS, "arch": registration.Arch},
	}); err != nil {
		return Node{}, err
	}
	if err := commit(tx, "Agent registration"); err != nil {
		return Node{}, err
	}
	return store.NodeByID(ctx, nodeID)
}

func (store *Store) AuthenticateAgent(ctx context.Context, nodeID string, credentialHash []byte) (Node, error) {
	return scanNode(store.db.QueryRowContext(ctx, `
	SELECT id, name, public_address, enabled, credential_hash, registered_at, last_seen_at,
	       hostname, agent_version, agent_os, agent_arch, agent_capabilities_json, agent_started_at_ns,
	       created_at, updated_at
	FROM nodes WHERE id = ? AND credential_hash = ?`, nodeID, credentialHash))
}

func (store *Store) RecordAgentHello(ctx context.Context, nodeID string, credentialHash []byte, agentVersion string,
	capabilities []string, startedAt, observedAt time.Time,
) error {
	encodedCapabilities, err := json.Marshal(capabilities)
	if err != nil {
		return fmt.Errorf("encode Agent capabilities: %w", err)
	}
	result, err := store.db.ExecContext(ctx, `
	UPDATE nodes SET agent_version = ?, agent_capabilities_json = ?, agent_started_at_ns = ?, updated_at = ?
	WHERE id = ? AND credential_hash = ?`, agentVersion, string(encodedCapabilities),
		startedAt.UTC().UnixNano(), unixTime(observedAt), nodeID, credentialHash)
	if err != nil {
		return fmt.Errorf("record Agent hello: %w", err)
	}
	return expectOneAgentUpdate(result, "Agent hello")
}

func (store *Store) RecordAgentHeartbeat(ctx context.Context, nodeID string, credentialHash []byte, agentVersion string, observedAt time.Time) error {
	result, err := store.db.ExecContext(ctx, `
UPDATE nodes SET last_seen_at = ?, agent_version = ?, updated_at = ?
WHERE id = ? AND credential_hash = ?`, unixTime(observedAt), agentVersion, unixTime(observedAt), nodeID, credentialHash)
	if err != nil {
		return fmt.Errorf("record Agent heartbeat: %w", err)
	}
	return expectOneAgentUpdate(result, "Agent heartbeat")
}

func expectOneAgentUpdate(result sql.Result, operation string) error {
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s result: %w", operation, err)
	}
	if updated != 1 {
		return ErrNotFound
	}
	return nil
}
