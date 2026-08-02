package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

const (
	AgentCommandPending   = "pending"
	AgentCommandSucceeded = "succeeded"
	AgentCommandFailed    = "failed"
	AgentCommandExpired   = "expired"
)

type AgentCommand struct {
	ID            string
	NodeID        string
	Kind          string
	Request       agentv1.Command
	RequestSHA256 string
	Status        string
	Attempts      int
	LastSentAt    *time.Time
	Result        *agentv1.CommandResult
	CompletedAt   *time.Time
	ExpiresAt     time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (store *Store) CreateAgentCommand(ctx context.Context, commandID, nodeID string, request agentv1.Command, now time.Time) (AgentCommand, error) {
	if err := protocol.ValidateIdempotencyKey(commandID); err != nil {
		return AgentCommand{}, fmt.Errorf("validate Agent command ID: %w", err)
	}
	digest, err := agentv1.CommandDigest(request)
	if err != nil {
		return AgentCommand{}, fmt.Errorf("validate Agent command: %w", err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return AgentCommand{}, fmt.Errorf("encode Agent command: %w", err)
	}
	auditAction := "node.command.create"
	auditTargetType := "agent_command"
	auditTargetID := commandID
	auditMetadata := map[string]any{"node_id": nodeID, "kind": request.Kind}
	if request.Kind == agentv1.CommandAgentUpdate {
		update, err := agentv1.DecodeAgentUpdateCommand(request)
		if err != nil {
			return AgentCommand{}, fmt.Errorf("validate Agent update command: %w", err)
		}
		auditAction = "node.agent_update.request"
		auditTargetType = "node"
		auditTargetID = nodeID
		auditMetadata["command_id"] = commandID
		auditMetadata["version"] = update.Version
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCommand{}, fmt.Errorf("begin Agent command creation: %w", err)
	}
	defer tx.Rollback()
	if exists, err := existsTx(ctx, tx, "nodes", nodeID); err != nil {
		return AgentCommand{}, err
	} else if !exists {
		return AgentCommand{}, ErrNotFound
	}
	if request.Kind == agentv1.CommandAgentUpdate {
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND kind = ? AND status = 'pending' AND expires_at <= ?`,
			unixTime(now), unixTime(now), nodeID, request.Kind, unixTime(now)); err != nil {
			return AgentCommand{}, fmt.Errorf("expire previous Agent update command: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
	INSERT INTO agent_commands(id, node_id, kind, request_json, request_sha256, expires_at, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING`, commandID, nodeID, request.Kind, string(raw), digest,
		unixTime(request.ExpiresAt), unixTime(now), unixTime(now))
	if err != nil {
		return AgentCommand{}, fmt.Errorf("insert Agent command: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return AgentCommand{}, fmt.Errorf("read Agent command creation result: %w", err)
	}
	if inserted == 0 {
		var existingNodeID, existingDigest string
		if err := tx.QueryRowContext(ctx, "SELECT node_id, request_sha256 FROM agent_commands WHERE id = ?", commandID).
			Scan(&existingNodeID, &existingDigest); errors.Is(err, sql.ErrNoRows) {
			return AgentCommand{}, ErrConflict
		} else if err != nil {
			return AgentCommand{}, fmt.Errorf("read existing Agent command: %w", err)
		}
		if existingNodeID != nodeID || existingDigest != digest {
			return AgentCommand{}, ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return AgentCommand{}, fmt.Errorf("commit duplicate Agent command: %w", err)
		}
		return store.AgentCommandByID(ctx, commandID)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: auditAction,
		TargetType: auditTargetType, TargetID: auditTargetID, Outcome: "success",
		Metadata: auditMetadata,
	}); err != nil {
		return AgentCommand{}, err
	}
	if err := commit(tx, "Agent command creation"); err != nil {
		return AgentCommand{}, err
	}
	return store.AgentCommandByID(ctx, commandID)
}

func (store *Store) AgentCommandByID(ctx context.Context, commandID string) (AgentCommand, error) {
	return scanAgentCommand(store.db.QueryRowContext(ctx, agentCommandSelect+" WHERE id = ?", commandID))
}

func (store *Store) LatestAgentCommandByKind(ctx context.Context, nodeID, kind string, now time.Time) (AgentCommand, error) {
	if _, err := store.db.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND kind = ? AND status = 'pending' AND expires_at <= ?`,
		unixTime(now), unixTime(now), nodeID, kind, unixTime(now)); err != nil {
		return AgentCommand{}, fmt.Errorf("expire Agent commands by kind: %w", err)
	}
	return scanAgentCommand(store.db.QueryRowContext(ctx, agentCommandSelect+`
 WHERE node_id = ? AND kind = ?
 ORDER BY agent_commands.created_at DESC, agent_commands.rowid DESC LIMIT 1`, nodeID, kind))
}

func (store *Store) NextAgentCommand(ctx context.Context, nodeID string, now time.Time) (AgentCommand, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCommand{}, fmt.Errorf("begin Agent command dispatch: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND status = 'pending' AND expires_at <= ?`,
		unixTime(now), unixTime(now), nodeID, unixTime(now)); err != nil {
		return AgentCommand{}, fmt.Errorf("expire undispatched Agent commands: %w", err)
	}
	value, err := scanAgentCommand(tx.QueryRowContext(ctx, agentCommandSelect+`
 JOIN nodes ON nodes.id = agent_commands.node_id
 WHERE agent_commands.node_id = ? AND agent_commands.status = 'pending' AND nodes.enabled = 1
 ORDER BY agent_commands.created_at, agent_commands.rowid LIMIT 1`, nodeID))
	if errors.Is(err, ErrNotFound) {
		if err := tx.Commit(); err != nil {
			return AgentCommand{}, fmt.Errorf("commit expired Agent commands: %w", err)
		}
		return AgentCommand{}, ErrNotFound
	}
	if err != nil {
		return AgentCommand{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentCommand{}, fmt.Errorf("commit Agent command dispatch lookup: %w", err)
	}
	return value, nil
}

func (store *Store) MarkAgentCommandSent(ctx context.Context, commandID, nodeID string, sentAt time.Time) error {
	result, err := store.db.ExecContext(ctx, `
UPDATE agent_commands SET attempts = attempts + 1, last_sent_at = ?, updated_at = ?
WHERE id = ? AND node_id = ? AND status = 'pending'`, unixTime(sentAt), unixTime(sentAt), commandID, nodeID)
	if err != nil {
		return fmt.Errorf("mark Agent command sent: %w", err)
	}
	return expectOneAgentUpdate(result, "Agent command delivery")
}

func (store *Store) CompleteAgentCommand(ctx context.Context, nodeID string, credentialHash []byte, result agentv1.CommandResult, now time.Time) error {
	if err := agentv1.ValidateCommandResult(result); err != nil {
		return fmt.Errorf("validate Agent command result: %w", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode Agent command result: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Agent command completion: %w", err)
	}
	defer tx.Rollback()
	var kind, digest, status, requestJSON string
	var existingResult sql.NullString
	err = tx.QueryRowContext(ctx, `
	SELECT agent_commands.kind, agent_commands.request_sha256, agent_commands.status, agent_commands.request_json, agent_commands.result_json
FROM agent_commands JOIN nodes ON nodes.id = agent_commands.node_id
WHERE agent_commands.id = ? AND agent_commands.node_id = ? AND nodes.enabled = 1 AND nodes.credential_hash = ?`,
		result.CommandID, nodeID, credentialHash).Scan(&kind, &digest, &status, &requestJSON, &existingResult)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read Agent command for completion: %w", err)
	}
	if digest != result.RequestSHA256 {
		return ErrConflict
	}
	auditAction := "node.command.complete"
	auditTargetType := "agent_command"
	auditTargetID := result.CommandID
	auditMetadata := map[string]any{"kind": kind}
	if kind == agentv1.CommandAgentUpdate {
		var request agentv1.Command
		if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
			return fmt.Errorf("decode completed Agent update command: %w", err)
		}
		update, err := agentv1.DecodeAgentUpdateCommand(request)
		if err != nil {
			return fmt.Errorf("validate completed Agent update command: %w", err)
		}
		if result.Status == agentv1.CommandStatusSucceeded {
			output, err := agentv1.DecodeAgentUpdateOutput(result.Output)
			if err != nil {
				return fmt.Errorf("validate Agent update result: %w", err)
			}
			if output.Version != update.Version {
				return fmt.Errorf("validate Agent update result: activated version %q does not match requested version %q", output.Version, update.Version)
			}
		}
		auditAction = "node.agent_update.complete"
		auditTargetType = "node"
		auditTargetID = nodeID
		auditMetadata["command_id"] = result.CommandID
		auditMetadata["version"] = update.Version
	}
	if status != AgentCommandPending {
		if (status == AgentCommandSucceeded || status == AgentCommandFailed) && existingResult.Valid && bytes.Equal([]byte(existingResult.String), raw) {
			return tx.Commit()
		}
		return ErrStateConflict
	}
	status = result.Status
	updated, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = ?, result_json = ?, completed_at = ?, updated_at = ?
WHERE id = ? AND node_id = ? AND status = 'pending'`, status, string(raw), unixTime(now), unixTime(now), result.CommandID, nodeID)
	if err != nil {
		return fmt.Errorf("complete Agent command: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("read Agent command completion result: %w", err)
	}
	if rows != 1 {
		return ErrStateConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "agent", ActorID: nodeID, Action: auditAction,
		TargetType: auditTargetType, TargetID: auditTargetID, Outcome: result.Status,
		Metadata: auditMetadata,
	}); err != nil {
		return err
	}
	return commit(tx, "Agent command completion")
}

const agentCommandSelect = `
SELECT agent_commands.id, agent_commands.node_id, agent_commands.kind, agent_commands.request_json,
       agent_commands.request_sha256, agent_commands.status, agent_commands.attempts,
       agent_commands.last_sent_at, agent_commands.result_json, agent_commands.completed_at,
       agent_commands.expires_at, agent_commands.created_at, agent_commands.updated_at
FROM agent_commands`

func scanAgentCommand(row rowScanner) (AgentCommand, error) {
	var value AgentCommand
	var request []byte
	var result sql.NullString
	var lastSentAt, completedAt sql.NullInt64
	var expiresAt, createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.NodeID, &value.Kind, &request, &value.RequestSHA256,
		&value.Status, &value.Attempts, &lastSentAt, &result, &completedAt,
		&expiresAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentCommand{}, ErrNotFound
		}
		return AgentCommand{}, fmt.Errorf("scan Agent command: %w", err)
	}
	if err := json.Unmarshal(request, &value.Request); err != nil {
		return AgentCommand{}, fmt.Errorf("decode Agent command request: %w", err)
	}
	if result.Valid {
		var decoded agentv1.CommandResult
		if err := json.Unmarshal([]byte(result.String), &decoded); err != nil {
			return AgentCommand{}, fmt.Errorf("decode Agent command result: %w", err)
		}
		value.Result = &decoded
	}
	value.LastSentAt = nullableTime(lastSentAt)
	value.CompletedAt = nullableTime(completedAt)
	value.ExpiresAt = fromUnix(expiresAt)
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
