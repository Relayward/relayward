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
	ID               string
	NodeID           string
	Kind             string
	Request          agentv1.Command
	RequestEncrypted bool
	ScopeKey         string
	RequestSHA256    string
	Status           string
	Attempts         int
	LastSentAt       *time.Time
	Result           *agentv1.CommandResult
	CompletedAt      *time.Time
	ExpiresAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

const (
	AgentCommandSecretOwnerType = "agent_command"
	AgentCommandRequestSecret   = "request"
)

func (store *Store) CreateAgentCommand(ctx context.Context, commandID, nodeID string, request agentv1.Command, now time.Time) (AgentCommand, error) {
	if request.Kind == agentv1.CommandPluginReconcile {
		return AgentCommand{}, errors.New("plugin reconcile commands must use encrypted storage")
	}
	return store.createAgentCommand(ctx, commandID, nodeID, request, now)
}

func (store *Store) createAgentCommand(ctx context.Context, commandID, nodeID string, request agentv1.Command, now time.Time) (AgentCommand, error) {
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
	INSERT INTO agent_commands(id, node_id, kind, request_json, request_sha256, request_encrypted, scope_key, expires_at, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, commandID, nodeID, request.Kind, string(raw), digest, 0, "",
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
WHERE node_id = ? AND kind = ? AND status = 'pending' AND expires_at <= ?
  AND (kind NOT IN (?, ?) OR attempts = 0)`,
		unixTime(now), unixTime(now), nodeID, kind, unixTime(now),
		agentv1.CommandPluginReconcile, agentv1.CommandPolicyReconcile); err != nil {
		return AgentCommand{}, fmt.Errorf("expire Agent commands by kind: %w", err)
	}
	return scanAgentCommand(store.db.QueryRowContext(ctx, agentCommandSelect+`
 WHERE node_id = ? AND kind = ?
 ORDER BY agent_commands.created_at DESC, agent_commands.rowid DESC LIMIT 1`, nodeID, kind))
}

func (store *Store) ListAgentCommands(ctx context.Context, nodeID string, limit int, now time.Time) ([]AgentCommand, error) {
	if limit < 1 {
		return nil, errors.New("Agent command list limit must be positive")
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND status = 'pending' AND expires_at <= ?
  AND (kind NOT IN (?, ?) OR attempts = 0)`,
		unixTime(now), unixTime(now), nodeID, unixTime(now),
		agentv1.CommandPluginReconcile, agentv1.CommandPolicyReconcile); err != nil {
		return nil, fmt.Errorf("expire Agent commands before listing: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, agentCommandSelect+`
 WHERE agent_commands.node_id = ?
 ORDER BY agent_commands.created_at DESC, agent_commands.rowid DESC
 LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list Agent commands: %w", err)
	}
	defer rows.Close()
	values := make([]AgentCommand, 0, limit)
	for rows.Next() {
		value, err := scanAgentCommand(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent commands: %w", err)
	}
	return values, nil
}

func (store *Store) NextAgentCommand(ctx context.Context, nodeID string, now time.Time) (AgentCommand, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCommand{}, fmt.Errorf("begin Agent command dispatch: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND status = 'pending' AND expires_at <= ?
  AND (kind NOT IN (?, ?) OR attempts = 0)`,
		unixTime(now), unixTime(now), nodeID, unixTime(now),
		agentv1.CommandPluginReconcile, agentv1.CommandPolicyReconcile); err != nil {
		return AgentCommand{}, fmt.Errorf("expire undispatched Agent commands: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM secrets
WHERE owner_type = ? AND name = ? AND owner_id IN (
    SELECT id FROM agent_commands WHERE node_id = ? AND status = 'expired'
)`, AgentCommandSecretOwnerType, AgentCommandRequestSecret, nodeID); err != nil {
		return AgentCommand{}, fmt.Errorf("delete expired Agent command secrets: %w", err)
	}
	if err := cleanExpiredPluginCommandsTx(ctx, tx, now); err != nil {
		return AgentCommand{}, err
	}
	value, err := scanAgentCommand(tx.QueryRowContext(ctx, agentCommandSelect+`
 JOIN nodes ON nodes.id = agent_commands.node_id
 WHERE agent_commands.node_id = ? AND agent_commands.status = 'pending'
   AND (nodes.enabled = 1 OR agent_commands.kind = ?)
 ORDER BY CASE WHEN agent_commands.kind = ? THEN 0 ELSE 1 END,
          agent_commands.created_at, agent_commands.rowid LIMIT 1`,
		nodeID, agentv1.CommandPolicyReconcile, agentv1.CommandPolicyReconcile))
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
	return store.completeAgentCommand(ctx, nodeID, credentialHash, result, nil, now)
}

func (store *Store) CompleteEncryptedAgentCommand(ctx context.Context, nodeID string, credentialHash []byte,
	result agentv1.CommandResult, request agentv1.Command, now time.Time,
) error {
	return store.completeAgentCommand(ctx, nodeID, credentialHash, result, &request, now)
}

func (store *Store) completeAgentCommand(ctx context.Context, nodeID string, credentialHash []byte,
	result agentv1.CommandResult, decryptedRequest *agentv1.Command, now time.Time,
) error {
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
	var kind, digest, status, requestJSON, scopeKey string
	var requestEncrypted int
	var existingResult sql.NullString
	err = tx.QueryRowContext(ctx, `
	SELECT agent_commands.kind, agent_commands.request_sha256, agent_commands.status,
       agent_commands.request_json, agent_commands.request_encrypted, agent_commands.scope_key,
       agent_commands.result_json
FROM agent_commands JOIN nodes ON nodes.id = agent_commands.node_id
WHERE agent_commands.id = ? AND agent_commands.node_id = ? AND nodes.credential_hash = ?`,
		result.CommandID, nodeID, credentialHash).Scan(
		&kind, &digest, &status, &requestJSON, &requestEncrypted, &scopeKey, &existingResult,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read Agent command for completion: %w", err)
	}
	if digest != result.RequestSHA256 {
		return ErrConflict
	}
	if status != AgentCommandPending {
		if (status == AgentCommandSucceeded || status == AgentCommandFailed) && existingResult.Valid && bytes.Equal([]byte(existingResult.String), raw) {
			return tx.Commit()
		}
		return ErrStateConflict
	}
	if (requestEncrypted != 0) != (decryptedRequest != nil) {
		return ErrStateConflict
	}
	var request agentv1.Command
	if decryptedRequest != nil {
		request = *decryptedRequest
		requestDigest, err := agentv1.CommandDigest(request)
		if err != nil {
			return fmt.Errorf("validate decrypted Agent command: %w", err)
		}
		if requestDigest != digest || request.Kind != kind {
			return ErrConflict
		}
	} else if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		return fmt.Errorf("decode completed Agent command: %w", err)
	}
	auditAction := "node.command.complete"
	auditTargetType := "agent_command"
	auditTargetID := result.CommandID
	auditMetadata := map[string]any{"kind": kind}
	if kind == agentv1.CommandAgentUpdate {
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
	} else if kind == agentv1.CommandPluginReconcile {
		reconcile, err := agentv1.DecodePluginReconcileCommand(request)
		if err != nil {
			return fmt.Errorf("validate completed plugin reconcile command: %w", err)
		}
		if reconcile.PluginID != scopeKey {
			return ErrConflict
		}
		if result.Status == agentv1.CommandStatusSucceeded {
			output, err := agentv1.DecodePluginReconcileOutput(result.Output)
			if err != nil {
				return fmt.Errorf("validate plugin reconcile result: %w", err)
			}
			configurationSHA256 := ""
			if reconcile.DesiredState != agentv1.PluginStateAbsent {
				configurationSHA256, err = agentv1.PluginConfigurationDigest(reconcile.Configuration)
				if err != nil {
					return fmt.Errorf("digest completed plugin configuration: %w", err)
				}
			}
			if output.PluginID != reconcile.PluginID || output.Generation != reconcile.Generation ||
				output.State != reconcile.DesiredState || output.Version != reconcile.Version ||
				output.ConfigurationSHA256 != configurationSHA256 {
				return errors.New("plugin reconcile result does not match requested state")
			}
		}
		auditAction = "node.plugin_reconcile.complete"
		auditTargetType = "node_plugin_instance"
		auditTargetID = NodePluginSecretOwnerID(nodeID, reconcile.PluginID)
		auditMetadata = map[string]any{
			"command_id": result.CommandID, "node_id": nodeID, "plugin_id": reconcile.PluginID,
			"generation": reconcile.Generation, "desired_state": reconcile.DesiredState,
			"version": reconcile.Version,
		}
		if err := updateNodePluginReconcileResultTx(ctx, tx, nodeID, reconcile, result, now); err != nil {
			return err
		}
	} else if kind == agentv1.CommandPolicyReconcile {
		reconcile, err := agentv1.DecodePolicyReconcileCommand(request)
		if err != nil {
			return fmt.Errorf("validate completed policy reconcile command: %w", err)
		}
		if result.Status == agentv1.CommandStatusSucceeded {
			output, err := agentv1.DecodePolicyReconcileOutput(result.Output)
			if err != nil {
				return fmt.Errorf("validate policy reconcile result: %w", err)
			}
			if output.Generation != reconcile.Generation || output.AuthorizationCount != uint32(len(reconcile.Authorizations)) {
				return errors.New("policy reconcile result does not match requested state")
			}
		}
		auditAction = "node.policy_reconcile.complete"
		auditTargetType = "node"
		auditTargetID = nodeID
		auditMetadata = map[string]any{
			"command_id": result.CommandID, "generation": reconcile.Generation,
			"authorization_count": len(reconcile.Authorizations),
		}
		if err := updatePolicyReconcileResultTx(ctx, tx, nodeID, reconcile, result, now); err != nil {
			return err
		}
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
	if requestEncrypted != 0 {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM secrets WHERE owner_type = ? AND owner_id = ? AND name = ?`,
			AgentCommandSecretOwnerType, result.CommandID, AgentCommandRequestSecret); err != nil {
			return fmt.Errorf("delete completed Agent command secret: %w", err)
		}
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "agent", ActorID: nodeID, Action: auditAction,
		TargetType: auditTargetType, TargetID: auditTargetID, Outcome: agentCommandAuditOutcome(result.Status),
		Metadata: auditMetadata,
	}); err != nil {
		return err
	}
	return commit(tx, "Agent command completion")
}

func agentCommandAuditOutcome(status string) string {
	if status == agentv1.CommandStatusSucceeded {
		return "success"
	}
	return "failure"
}

func updatePolicyReconcileResultTx(ctx context.Context, tx *sql.Tx, nodeID string,
	reconcile agentv1.PolicyReconcileCommand, result agentv1.CommandResult, now time.Time,
) error {
	if result.Status == agentv1.CommandStatusSucceeded {
		updated, err := tx.ExecContext(ctx, `
UPDATE node_policy_state SET applied_generation = ?, reconcile_status = 'applied',
    last_problem_json = NULL, retry_after = NULL, updated_at = ?
WHERE node_id = ? AND desired_generation = ? AND last_command_id = ?`,
			int64(reconcile.Generation), unixTime(now), nodeID, int64(reconcile.Generation), result.CommandID)
		if err != nil {
			return fmt.Errorf("record applied policy reconciliation: %w", err)
		}
		return expectOneAgentUpdate(updated, "policy reconciliation success")
	}
	problemJSON, err := json.Marshal(result.Problem)
	if err != nil {
		return fmt.Errorf("encode policy reconciliation problem: %w", err)
	}
	retryAfter := any(nil)
	if result.Problem != nil && result.Problem.Retryable {
		retryAfter = unixTime(now.Add(policyRetryDelay))
	}
	updated, err := tx.ExecContext(ctx, `
UPDATE node_policy_state SET reconcile_status = 'failed', last_problem_json = ?,
    retry_after = ?, updated_at = ?
WHERE node_id = ? AND desired_generation = ? AND last_command_id = ?`,
		string(problemJSON), retryAfter, unixTime(now), nodeID, int64(reconcile.Generation), result.CommandID)
	if err != nil {
		return fmt.Errorf("record failed policy reconciliation: %w", err)
	}
	return expectOneAgentUpdate(updated, "policy reconciliation failure")
}

func updateNodePluginReconcileResultTx(ctx context.Context, tx *sql.Tx, nodeID string,
	reconcile agentv1.PluginReconcileCommand, result agentv1.CommandResult, now time.Time,
) error {
	problemJSON := any(nil)
	if result.Problem != nil {
		raw, err := json.Marshal(result.Problem)
		if err != nil {
			return fmt.Errorf("encode plugin reconcile problem: %w", err)
		}
		problemJSON = string(raw)
	}
	if result.Status == agentv1.CommandStatusFailed {
		updated, err := tx.ExecContext(ctx, `
UPDATE node_plugin_instances SET reconcile_status = 'failed', last_problem_json = ?, updated_at = ?
WHERE node_id = ? AND plugin_id = ? AND generation = ?`,
			problemJSON, unixTime(now), nodeID, reconcile.PluginID, int64(reconcile.Generation))
		if err != nil {
			return fmt.Errorf("record failed plugin reconciliation: %w", err)
		}
		return expectOneAgentUpdate(updated, "plugin reconciliation failure")
	}
	configurationSHA256 := ""
	health := agentv1.PluginHealthUnknown
	if reconcile.DesiredState != agentv1.PluginStateAbsent {
		var err error
		configurationSHA256, err = agentv1.PluginConfigurationDigest(reconcile.Configuration)
		if err != nil {
			return fmt.Errorf("digest reconciled plugin configuration: %w", err)
		}
		if reconcile.DesiredState == agentv1.PluginStateRunning {
			health = agentv1.PluginHealthHealthy
		}
	}
	var actualGeneration int64
	var actualObservedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
SELECT actual_generation, actual_observed_at_ns
FROM node_plugin_instances
WHERE node_id = ? AND plugin_id = ? AND generation = ?`,
		nodeID, reconcile.PluginID, int64(reconcile.Generation),
	).Scan(&actualGeneration, &actualObservedAt); errors.Is(err, sql.ErrNoRows) {
		return ErrStateConflict
	} else if err != nil {
		return fmt.Errorf("read plugin status before reconciliation success: %w", err)
	}
	resultObservedAt := result.CompletedAt.UTC().UnixNano()
	if actualGeneration > int64(reconcile.Generation) ||
		(actualGeneration == int64(reconcile.Generation) && actualObservedAt.Valid && actualObservedAt.Int64 > resultObservedAt) {
		updated, err := tx.ExecContext(ctx, `
UPDATE node_plugin_instances SET
    reconcile_status = 'succeeded', last_problem_json = NULL, updated_at = ?
WHERE node_id = ? AND plugin_id = ? AND generation = ?`,
			unixTime(now), nodeID, reconcile.PluginID, int64(reconcile.Generation))
		if err != nil {
			return fmt.Errorf("record successful plugin reconciliation: %w", err)
		}
		return expectOneAgentUpdate(updated, "plugin reconciliation success")
	}
	updated, err := tx.ExecContext(ctx, `
UPDATE node_plugin_instances SET
    active_version = NULLIF(?, ''), actual_state = ?, actual_generation = ?,
    actual_configuration_sha256 = ?, health = ?, reason = '', restart_count = 0,
    reconcile_status = 'succeeded', last_problem_json = NULL,
    actual_observed_at_ns = ?, updated_at = ?
WHERE node_id = ? AND plugin_id = ? AND generation = ?`,
		reconcile.Version, reconcile.DesiredState, int64(reconcile.Generation), configurationSHA256,
		health, resultObservedAt, unixTime(now), nodeID, reconcile.PluginID, int64(reconcile.Generation))
	if err != nil {
		return fmt.Errorf("record successful plugin reconciliation: %w", err)
	}
	if err := expectOneAgentUpdate(updated, "plugin reconciliation success"); err != nil {
		return err
	}
	if reconcile.DesiredState == agentv1.PluginStateAbsent {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM secrets WHERE owner_type = ? AND owner_id = ? AND name = ?`,
			NodePluginSecretOwnerType, NodePluginSecretOwnerID(nodeID, reconcile.PluginID), NodePluginConfigurationSecret); err != nil {
			return fmt.Errorf("delete removed plugin configuration: %w", err)
		}
	}
	return nil
}

const agentCommandSelect = `
SELECT agent_commands.id, agent_commands.node_id, agent_commands.kind, agent_commands.request_json,
	   agent_commands.request_encrypted, agent_commands.scope_key, agent_commands.request_sha256,
	   agent_commands.status, agent_commands.attempts,
       agent_commands.last_sent_at, agent_commands.result_json, agent_commands.completed_at,
       agent_commands.expires_at, agent_commands.created_at, agent_commands.updated_at
FROM agent_commands`

func scanAgentCommand(row rowScanner) (AgentCommand, error) {
	var value AgentCommand
	var request []byte
	var result sql.NullString
	var lastSentAt, completedAt sql.NullInt64
	var expiresAt, createdAt, updatedAt int64
	var requestEncrypted int
	if err := row.Scan(&value.ID, &value.NodeID, &value.Kind, &request, &requestEncrypted, &value.ScopeKey, &value.RequestSHA256,
		&value.Status, &value.Attempts, &lastSentAt, &result, &completedAt,
		&expiresAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentCommand{}, ErrNotFound
		}
		return AgentCommand{}, fmt.Errorf("scan Agent command: %w", err)
	}
	value.RequestEncrypted = requestEncrypted != 0
	if !value.RequestEncrypted {
		if err := json.Unmarshal(request, &value.Request); err != nil {
			return AgentCommand{}, fmt.Errorf("decode Agent command request: %w", err)
		}
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
