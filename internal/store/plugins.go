package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/Relayward/relayward-sdk/protocol"
)

const (
	NodePluginSecretOwnerType     = "node_plugin_instance"
	NodePluginConfigurationSecret = "desired_configuration"
)

var githubRepositoryPartPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type PluginInstallation struct {
	PluginID       string
	Repository     string
	Kind           string
	DesiredVersion string
	ActiveVersion  string
	Manifest       manifest.Manifest
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (store *Store) CreatePluginInstallation(ctx context.Context, value PluginInstallation, now time.Time) error {
	if err := manifest.Validate(value.Manifest); err != nil {
		return fmt.Errorf("validate plugin manifest: %w", err)
	}
	if value.PluginID != value.Manifest.ID || value.Kind != string(value.Manifest.Kind) ||
		value.DesiredVersion != value.Manifest.Version ||
		(value.ActiveVersion != "" && value.ActiveVersion != value.Manifest.Version) {
		return errors.New("plugin installation does not match its manifest")
	}
	if err := validatePluginRepository(value.Repository); err != nil {
		return err
	}
	switch value.State {
	case "pending", "active", "failed":
	default:
		return fmt.Errorf("unsupported plugin installation state %q", value.State)
	}
	manifestJSON, err := json.Marshal(value.Manifest)
	if err != nil {
		return fmt.Errorf("encode plugin manifest: %w", err)
	}
	permissionsJSON, err := json.Marshal(value.Manifest.Permissions)
	if err != nil {
		return fmt.Errorf("encode plugin permissions: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin installation creation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO plugin_installations(
    plugin_id, repository, kind, desired_version, active_version, manifest_json,
    permissions_json, state, created_at, updated_at
) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, value.PluginID, value.Repository, value.Kind, value.DesiredVersion,
		value.ActiveVersion, string(manifestJSON), string(permissionsJSON), value.State, unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert plugin installation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read plugin installation creation result: %w", err)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "plugin.install",
		TargetType: "plugin_installation", TargetID: value.PluginID, Outcome: "success",
		Metadata: map[string]any{"repository": value.Repository, "version": value.DesiredVersion, "state": value.State},
	}); err != nil {
		return err
	}
	return commit(tx, "plugin installation creation")
}

func validatePluginRepository(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("plugin repository must be an HTTPS github.com repository URL without credentials")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return errors.New("plugin repository must contain exactly an owner and repository")
	}
	for index, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return errors.New("plugin repository contains invalid path escaping")
		}
		if index == 1 {
			decoded = strings.TrimSuffix(decoded, ".git")
		}
		if !githubRepositoryPartPattern.MatchString(decoded) || decoded == "." || decoded == ".." {
			return errors.New("plugin repository owner or name is invalid")
		}
	}
	return nil
}

type NodePluginInstance struct {
	NodeID                     string
	NodeName                   string
	PluginID                   string
	PluginName                 string
	DesiredVersion             string
	ActiveVersion              string
	DesiredState               string
	ActualState                string
	Generation                 uint64
	DesiredConfigurationSHA256 string
	ArtifactSize               int64
	ArtifactSHA256             string
	ActualGeneration           uint64
	ActualConfigurationSHA256  string
	Health                     string
	Reason                     string
	RestartCount               uint64
	ReconcileStatus            string
	LastProblem                *protocol.Problem
	LastCommandID              string
	CommandStatus              string
	CommandAttempts            int
	CommandLastSentAt          *time.Time
	CommandCompletedAt         *time.Time
	ActualObservedAt           *time.Time
	UpdatedAt                  time.Time
}

type NodePluginDesired struct {
	NodeID                     string
	PluginID                   string
	Generation                 uint64
	DesiredState               string
	DesiredVersion             string
	DesiredConfigurationSHA256 string
	ArtifactSize               int64
	ArtifactSHA256             string
}

func NodePluginSecretOwnerID(nodeID, pluginID string) string {
	return nodeID + "/" + pluginID
}

func (store *Store) PluginInstallationByID(ctx context.Context, pluginID string) (PluginInstallation, error) {
	var value PluginInstallation
	var activeVersion sql.NullString
	var raw []byte
	var createdAt, updatedAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT plugin_id, repository, kind, desired_version, active_version, manifest_json, state, created_at, updated_at
FROM plugin_installations WHERE plugin_id = ?`, pluginID).Scan(
		&value.PluginID, &value.Repository, &value.Kind, &value.DesiredVersion, &activeVersion,
		&raw, &value.State, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginInstallation{}, ErrNotFound
	}
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("read plugin installation: %w", err)
	}
	decoded, err := manifest.Decode(bytes.NewReader(raw))
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("decode installed plugin manifest: %w", err)
	}
	value.ActiveVersion = activeVersion.String
	value.Manifest = decoded
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) ListNodePluginInstances(ctx context.Context) ([]NodePluginInstance, error) {
	rows, err := store.db.QueryContext(ctx, nodePluginInstanceSelect+`
ORDER BY json_extract(plugin_installations.manifest_json, '$.name') COLLATE NOCASE,
         nodes.name COLLATE NOCASE, node_plugin_instances.node_id`)
	if err != nil {
		return nil, fmt.Errorf("list node plugin instances: %w", err)
	}
	defer rows.Close()
	values := make([]NodePluginInstance, 0)
	for rows.Next() {
		value, err := scanNodePluginInstance(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node plugin instances: %w", err)
	}
	return values, nil
}

func (store *Store) NodePluginInstanceByID(ctx context.Context, nodeID, pluginID string) (NodePluginInstance, error) {
	return scanNodePluginInstance(store.db.QueryRowContext(ctx, nodePluginInstanceSelect+`
WHERE node_plugin_instances.node_id = ? AND node_plugin_instances.plugin_id = ?`, nodeID, pluginID))
}

func (store *Store) ApplyNodePluginDesired(ctx context.Context, desired NodePluginDesired, configurationCiphertext []byte,
	commandID string, command agentv1.Command, commandCiphertext []byte, now time.Time,
) (NodePluginInstance, error) {
	if err := protocol.ValidateIdempotencyKey(commandID); err != nil {
		return NodePluginInstance{}, fmt.Errorf("validate plugin reconcile command ID: %w", err)
	}
	reconcile, err := agentv1.DecodePluginReconcileCommand(command)
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("validate plugin reconcile command: %w", err)
	}
	if reconcile.PluginID != desired.PluginID || reconcile.Generation != desired.Generation ||
		reconcile.DesiredState != desired.DesiredState || reconcile.Version != desired.DesiredVersion {
		return NodePluginInstance{}, errors.New("plugin reconcile command does not match desired state")
	}
	configurationSHA256 := ""
	artifactSize := int64(0)
	artifactSHA256 := ""
	if reconcile.DesiredState != agentv1.PluginStateAbsent {
		configurationSHA256, err = agentv1.PluginConfigurationDigest(reconcile.Configuration)
		if err != nil {
			return NodePluginInstance{}, fmt.Errorf("digest desired plugin configuration: %w", err)
		}
		artifactSize = reconcile.Artifact.Size
		artifactSHA256 = reconcile.Artifact.SHA256
	}
	if desired.DesiredConfigurationSHA256 != configurationSHA256 || desired.ArtifactSize != artifactSize ||
		desired.ArtifactSHA256 != artifactSHA256 {
		return NodePluginInstance{}, errors.New("plugin reconcile command does not match desired state metadata")
	}
	digest, err := agentv1.CommandDigest(command)
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("digest plugin reconcile command: %w", err)
	}
	if len(commandCiphertext) == 0 || (desired.DesiredState != agentv1.PluginStateAbsent && len(configurationCiphertext) == 0) {
		return NodePluginInstance{}, errors.New("encrypted plugin state is required")
	}
	metadata, err := json.Marshal(map[string]any{
		"kind": command.Kind, "plugin_id": desired.PluginID, "generation": desired.Generation,
		"desired_state": desired.DesiredState, "version": desired.DesiredVersion,
	})
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("encode plugin reconcile metadata: %w", err)
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("begin node plugin reconciliation: %w", err)
	}
	defer tx.Rollback()
	var nodeReady int
	if err := tx.QueryRowContext(ctx, `
SELECT 1 FROM nodes WHERE id = ? AND enabled = 1 AND credential_hash IS NOT NULL`, desired.NodeID).Scan(&nodeReady); errors.Is(err, sql.ErrNoRows) {
		return NodePluginInstance{}, ErrNotFound
	} else if err != nil {
		return NodePluginInstance{}, fmt.Errorf("check node plugin target: %w", err)
	}
	var installationState string
	var activeVersion sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT state, active_version FROM plugin_installations WHERE plugin_id = ?`, desired.PluginID).Scan(
		&installationState, &activeVersion,
	); errors.Is(err, sql.ErrNoRows) {
		return NodePluginInstance{}, ErrNotFound
	} else if err != nil {
		return NodePluginInstance{}, fmt.Errorf("check plugin installation: %w", err)
	}
	if desired.DesiredState != agentv1.PluginStateAbsent &&
		(installationState != "active" || activeVersion.String != desired.DesiredVersion) {
		return NodePluginInstance{}, ErrStateConflict
	}

	var currentGeneration int64
	err = tx.QueryRowContext(ctx, `
SELECT generation FROM node_plugin_instances WHERE node_id = ? AND plugin_id = ?`, desired.NodeID, desired.PluginID).Scan(&currentGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		currentGeneration = 0
	} else if err != nil {
		return NodePluginInstance{}, fmt.Errorf("read current node plugin generation: %w", err)
	}
	if desired.Generation != uint64(currentGeneration)+1 {
		return NodePluginInstance{}, ErrStateConflict
	}

	if err := expirePluginCommandsTx(ctx, tx, desired.NodeID, desired.PluginID, now); err != nil {
		return NodePluginInstance{}, err
	}
	var pending int
	err = tx.QueryRowContext(ctx, `
SELECT 1 FROM agent_commands
WHERE node_id = ? AND kind = ? AND scope_key = ? AND status = 'pending'`,
		desired.NodeID, agentv1.CommandPluginReconcile, desired.PluginID).Scan(&pending)
	if err == nil {
		return NodePluginInstance{}, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return NodePluginInstance{}, fmt.Errorf("check pending plugin reconcile command: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO node_plugin_instances(
    node_id, plugin_id, desired_version, active_version, desired_state, actual_state, updated_at,
    generation, desired_configuration_sha256, artifact_size, artifact_sha256, actual_generation,
    actual_configuration_sha256, health, reason, restart_count, reconcile_status,
    last_problem_json, last_command_id, actual_observed_at_ns
) VALUES (?, ?, ?, NULL, ?, 'absent', ?, ?, ?, ?, ?, 0, '', 'unknown', '', 0, 'pending', NULL, ?, NULL)
ON CONFLICT(node_id, plugin_id) DO UPDATE SET
    desired_version = excluded.desired_version,
    desired_state = excluded.desired_state,
    updated_at = excluded.updated_at,
    generation = excluded.generation,
    desired_configuration_sha256 = excluded.desired_configuration_sha256,
    artifact_size = excluded.artifact_size,
    artifact_sha256 = excluded.artifact_sha256,
    reconcile_status = 'pending',
    last_problem_json = NULL,
    last_command_id = excluded.last_command_id`,
		desired.NodeID, desired.PluginID, desired.DesiredVersion, desired.DesiredState, unixTime(now),
		int64(desired.Generation), desired.DesiredConfigurationSHA256, desired.ArtifactSize,
		desired.ArtifactSHA256, commandID,
	)
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("store node plugin desired state: %w", err)
	}

	ownerID := NodePluginSecretOwnerID(desired.NodeID, desired.PluginID)
	if desired.DesiredState != agentv1.PluginStateAbsent {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET
    ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
			NodePluginSecretOwnerType, ownerID, NodePluginConfigurationSecret, configurationCiphertext, unixTime(now)); err != nil {
			return NodePluginInstance{}, fmt.Errorf("store desired plugin configuration: %w", err)
		}
	}

	insertedCommand, err := tx.ExecContext(ctx, `
INSERT INTO agent_commands(
    id, node_id, kind, request_json, request_sha256, request_encrypted, scope_key,
    expires_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, commandID, desired.NodeID, command.Kind,
		string(metadata), digest, desired.PluginID, unixTime(command.ExpiresAt), unixTime(now), unixTime(now))
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("insert plugin reconcile command: %w", err)
	}
	inserted, err := insertedCommand.RowsAffected()
	if err != nil {
		return NodePluginInstance{}, fmt.Errorf("read plugin reconcile command creation result: %w", err)
	}
	if inserted != 1 {
		return NodePluginInstance{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES (?, ?, ?, ?, ?)`, AgentCommandSecretOwnerType, commandID, AgentCommandRequestSecret,
		commandCiphertext, unixTime(now)); err != nil {
		return NodePluginInstance{}, fmt.Errorf("store encrypted plugin reconcile command: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "node.plugin_reconcile.request",
		TargetType: "node_plugin_instance", TargetID: ownerID, Outcome: "success",
		Metadata: map[string]any{
			"command_id": commandID, "node_id": desired.NodeID, "plugin_id": desired.PluginID,
			"generation": desired.Generation, "desired_state": desired.DesiredState, "version": desired.DesiredVersion,
		},
	}); err != nil {
		return NodePluginInstance{}, err
	}
	if err := commit(tx, "node plugin reconciliation"); err != nil {
		return NodePluginInstance{}, err
	}
	return store.NodePluginInstanceByID(ctx, desired.NodeID, desired.PluginID)
}

func (store *Store) RecordNodePluginStatus(ctx context.Context, nodeID string, status agentv1.PluginStatusEvent, observedAt, receivedAt time.Time) error {
	if err := agentv1.ValidatePluginStatusEvent(status); err != nil {
		return fmt.Errorf("validate plugin status: %w", err)
	}
	var desiredGeneration, actualGeneration int64
	err := store.db.QueryRowContext(ctx, `
SELECT generation, actual_generation FROM node_plugin_instances WHERE node_id = ? AND plugin_id = ?`, nodeID, status.PluginID).Scan(
		&desiredGeneration, &actualGeneration,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read node plugin for status: %w", err)
	}
	if status.Generation != uint64(desiredGeneration) && status.Generation != uint64(actualGeneration) {
		return nil
	}
	result, err := store.db.ExecContext(ctx, `
UPDATE node_plugin_instances SET
    active_version = NULLIF(?, ''), actual_state = ?, actual_generation = ?,
    actual_configuration_sha256 = ?, health = ?, reason = ?, restart_count = ?,
    actual_observed_at_ns = ?, updated_at = ?
WHERE node_id = ? AND plugin_id = ?
  AND (actual_generation < ? OR
       (actual_generation = ? AND (actual_observed_at_ns IS NULL OR actual_observed_at_ns <= ?)))`,
		status.Version, status.State, int64(status.Generation), status.ConfigurationSHA256,
		status.Health, status.Reason, int64(status.RestartCount), observedAt.UTC().UnixNano(), unixTime(receivedAt),
		nodeID, status.PluginID, int64(status.Generation), int64(status.Generation), observedAt.UTC().UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("record node plugin status: %w", err)
	}
	if _, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read node plugin status result: %w", err)
	}
	return nil
}

func (store *Store) ExpireNodePluginCommands(ctx context.Context, now time.Time) error {
	var expired int
	err := store.db.QueryRowContext(ctx, `
SELECT 1 FROM agent_commands
WHERE kind = ? AND status = 'pending' AND attempts = 0 AND expires_at <= ? LIMIT 1`,
		agentv1.CommandPluginReconcile, unixTime(now)).Scan(&expired)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check expired plugin reconcile commands: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin command expiry: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE kind = ? AND status = 'pending' AND attempts = 0 AND expires_at <= ?`,
		unixTime(now), unixTime(now), agentv1.CommandPluginReconcile, unixTime(now)); err != nil {
		return fmt.Errorf("expire plugin reconcile commands: %w", err)
	}
	if err := cleanExpiredPluginCommandsTx(ctx, tx, now); err != nil {
		return err
	}
	return commit(tx, "plugin command expiry")
}

func expirePluginCommandsTx(ctx context.Context, tx *sql.Tx, nodeID, pluginID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE node_id = ? AND kind = ? AND scope_key = ? AND status = 'pending'
  AND attempts = 0 AND expires_at <= ?`,
		unixTime(now), unixTime(now), nodeID, agentv1.CommandPluginReconcile, pluginID, unixTime(now)); err != nil {
		return fmt.Errorf("expire plugin reconcile commands: %w", err)
	}
	return cleanExpiredPluginCommandsTx(ctx, tx, now)
}

func cleanExpiredPluginCommandsTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
DELETE FROM secrets
WHERE owner_type = ? AND name = ? AND owner_id IN (
    SELECT id FROM agent_commands
	    WHERE kind = ? AND status = 'expired'
)`, AgentCommandSecretOwnerType, AgentCommandRequestSecret, agentv1.CommandPluginReconcile); err != nil {
		return fmt.Errorf("delete expired plugin reconcile secrets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE node_plugin_instances SET reconcile_status = 'expired', updated_at = ?
WHERE reconcile_status = 'pending' AND last_command_id IN (
    SELECT id FROM agent_commands WHERE kind = ? AND status = 'expired'
)`, unixTime(now), agentv1.CommandPluginReconcile); err != nil {
		return fmt.Errorf("mark expired plugin reconciliations: %w", err)
	}
	return nil
}

const nodePluginInstanceSelect = `
SELECT node_plugin_instances.node_id, node_plugin_instances.plugin_id,
       nodes.name,
       json_extract(plugin_installations.manifest_json, '$.name'),
       node_plugin_instances.desired_version, node_plugin_instances.active_version,
       node_plugin_instances.desired_state, node_plugin_instances.actual_state,
       node_plugin_instances.generation, node_plugin_instances.desired_configuration_sha256,
       node_plugin_instances.artifact_size, node_plugin_instances.artifact_sha256,
       node_plugin_instances.actual_generation, node_plugin_instances.actual_configuration_sha256,
       node_plugin_instances.health, node_plugin_instances.reason, node_plugin_instances.restart_count,
       node_plugin_instances.reconcile_status, node_plugin_instances.last_problem_json,
       node_plugin_instances.last_command_id,
       COALESCE(agent_commands.status, ''), COALESCE(agent_commands.attempts, 0),
       agent_commands.last_sent_at, agent_commands.completed_at,
       node_plugin_instances.actual_observed_at_ns, node_plugin_instances.updated_at
FROM node_plugin_instances
JOIN nodes ON nodes.id = node_plugin_instances.node_id
JOIN plugin_installations ON plugin_installations.plugin_id = node_plugin_instances.plugin_id
LEFT JOIN agent_commands ON agent_commands.id = node_plugin_instances.last_command_id
`

func scanNodePluginInstance(row rowScanner) (NodePluginInstance, error) {
	var value NodePluginInstance
	var activeVersion, problem sql.NullString
	var lastSentAt, completedAt, actualObservedAt sql.NullInt64
	var generation, actualGeneration, restartCount int64
	var updatedAt int64
	if err := row.Scan(
		&value.NodeID, &value.PluginID, &value.NodeName, &value.PluginName, &value.DesiredVersion, &activeVersion,
		&value.DesiredState, &value.ActualState, &generation, &value.DesiredConfigurationSHA256,
		&value.ArtifactSize, &value.ArtifactSHA256, &actualGeneration, &value.ActualConfigurationSHA256,
		&value.Health, &value.Reason, &restartCount, &value.ReconcileStatus, &problem,
		&value.LastCommandID, &value.CommandStatus, &value.CommandAttempts, &lastSentAt, &completedAt,
		&actualObservedAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodePluginInstance{}, ErrNotFound
		}
		return NodePluginInstance{}, fmt.Errorf("scan node plugin instance: %w", err)
	}
	if problem.Valid {
		var decoded protocol.Problem
		if err := json.Unmarshal([]byte(problem.String), &decoded); err != nil {
			return NodePluginInstance{}, fmt.Errorf("decode node plugin problem: %w", err)
		}
		value.LastProblem = &decoded
	}
	value.ActiveVersion = activeVersion.String
	value.Generation = uint64(generation)
	value.ActualGeneration = uint64(actualGeneration)
	value.RestartCount = uint64(restartCount)
	value.CommandLastSentAt = nullableTime(lastSentAt)
	value.CommandCompletedAt = nullableTime(completedAt)
	if actualObservedAt.Valid {
		observedAt := time.Unix(0, actualObservedAt.Int64).UTC()
		value.ActualObservedAt = &observedAt
	}
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
