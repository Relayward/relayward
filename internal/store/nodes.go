package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Node struct {
	ID             string
	Name           string
	PublicAddress  string
	Enabled        bool
	CredentialHash []byte
	RegisteredAt   *time.Time
	LastSeenAt     *time.Time
	Hostname       string
	AgentVersion   string
	AgentOS        string
	AgentArch      string
	Capabilities   []string
	AgentStartedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (store *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, name, public_address, enabled, credential_hash, registered_at, last_seen_at,
	       hostname, agent_version, agent_os, agent_arch, agent_capabilities_json, agent_started_at_ns,
	       created_at, updated_at
FROM nodes ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	values := make([]Node, 0)
	for rows.Next() {
		value, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	return values, nil
}

func (store *Store) NodeByID(ctx context.Context, id string) (Node, error) {
	return scanNode(store.db.QueryRowContext(ctx, `
SELECT id, name, public_address, enabled, credential_hash, registered_at, last_seen_at,
	       hostname, agent_version, agent_os, agent_arch, agent_capabilities_json, agent_started_at_ns,
	       created_at, updated_at
FROM nodes WHERE id = ?`, id))
}

func (store *Store) CreateNode(ctx context.Context, value Node, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
INSERT INTO nodes(id, name, public_address, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, value.ID, value.Name, value.PublicAddress, boolInt(value.Enabled), unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert node: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read node creation result: %w", err)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "node.create", TargetType: "node", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "node creation")
}

func (store *Store) UpdateNode(ctx context.Context, value Node, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE OR IGNORE nodes SET name = ?, public_address = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		value.Name, value.PublicAddress, boolInt(value.Enabled), unixTime(now), value.ID)
	if err != nil {
		return fmt.Errorf("update node: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read node update result: %w", err)
	}
	if updated != 1 {
		exists, err := existsTx(ctx, tx, "nodes", value.ID)
		if err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "node.update", TargetType: "node", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "node update")
}

func (store *Store) DeleteNode(ctx context.Context, id string, now time.Time) error {
	return store.deleteEntity(ctx, "nodes", "node", id, "node.delete", now)
}

func (store *Store) RevokeNodeCredential(ctx context.Context, id string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node credential revocation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE nodes SET credential_hash = NULL, registered_at = NULL, last_seen_at = NULL,
    hostname = '', agent_version = '', agent_os = '', agent_arch = '',
    agent_capabilities_json = '[]', agent_started_at_ns = NULL, updated_at = ?
WHERE id = ? AND credential_hash IS NOT NULL`, unixTime(now), id)
	if err != nil {
		return fmt.Errorf("revoke node credential: %w", err)
	}
	revoked, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read node credential revocation result: %w", err)
	}
	if revoked != 1 {
		exists, err := existsTx(ctx, tx, "nodes", id)
		if err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE node_registration_tokens SET used_at = ? WHERE node_id = ? AND used_at IS NULL`, unixTime(now), id); err != nil {
		return fmt.Errorf("invalidate node registration tokens after credential revocation: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "node.credential.revoke",
		TargetType: "node", TargetID: id, Outcome: "success",
	}); err != nil {
		return err
	}
	return commit(tx, "node credential revocation")
}

func (store *Store) CreateNodeRegistrationToken(ctx context.Context, nodeID string, tokenHash []byte, expiresAt, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node registration token creation: %w", err)
	}
	defer tx.Rollback()

	exists, err := existsTx(ctx, tx, "nodes", nodeID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE node_registration_tokens SET used_at = ? WHERE node_id = ? AND used_at IS NULL`, unixTime(now), nodeID); err != nil {
		return fmt.Errorf("invalidate node registration tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO node_registration_tokens(token_hash, node_id, created_at, expires_at)
VALUES (?, ?, ?, ?)`, tokenHash, nodeID, unixTime(now), unixTime(expiresAt)); err != nil {
		return fmt.Errorf("insert node registration token: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "node.registration_token.create", TargetType: "node", TargetID: nodeID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "node registration token creation")
}

func scanNode(row rowScanner) (Node, error) {
	var value Node
	var enabled int
	var credentialHash []byte
	var registeredAt, lastSeenAt, agentStartedAt sql.NullInt64
	var capabilities []byte
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &value.PublicAddress, &enabled, &credentialHash,
		&registeredAt, &lastSeenAt, &value.Hostname, &value.AgentVersion, &value.AgentOS, &value.AgentArch,
		&capabilities, &agentStartedAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNotFound
		}
		return Node{}, fmt.Errorf("scan node: %w", err)
	}
	value.Enabled = enabled != 0
	value.CredentialHash = credentialHash
	value.RegisteredAt = nullableTime(registeredAt)
	value.LastSeenAt = nullableTime(lastSeenAt)
	if agentStartedAt.Valid {
		startedAt := time.Unix(0, agentStartedAt.Int64).UTC()
		value.AgentStartedAt = &startedAt
	}
	if err := json.Unmarshal(capabilities, &value.Capabilities); err != nil {
		return Node{}, fmt.Errorf("decode node capabilities: %w", err)
	}
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
