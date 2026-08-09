package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	policyv1 "github.com/Relayward/relayward-sdk/policy/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

const policyRetryDelay = 30 * time.Second

type NodePolicySnapshot struct {
	NodeID            string
	NodeEnabled       bool
	AgentCapabilities []string
	AgentStartedAt    *time.Time
	Authorizations    []agentv1.AuthorizationPolicy
	SHA256            string
}

type NodePolicyState struct {
	NodeID               string
	DesiredGeneration    uint64
	DesiredSHA256        string
	AppliedGeneration    uint64
	ReconcileStatus      string
	LastProblem          *protocol.Problem
	LastCommandID        string
	IssuedAgentStartedAt *time.Time
	RetryAfter           *time.Time
	UpdatedAt            time.Time
}

type AuthorizationPolicyStatus struct {
	AuthorizationID string
	Generation      uint64
	Period          policyv1.Period
	UploadBytes     uint64
	DownloadBytes   uint64
	ServicesEnabled bool
	Reason          string
	ActiveIPCount   uint32
	BlockedIPCount  uint32
	ObservedAt      time.Time
	UpdatedAt       time.Time
}

type TrafficPeriod struct {
	AuthorizationID string
	Period          policyv1.Period
	Revision        uint64
	UploadBytes     uint64
	DownloadBytes   uint64
	EnforcedAt      *time.Time
	ObservedAt      time.Time
	UpdatedAt       time.Time
}

type TrafficSnapshotSource struct {
	NodeID     string
	StreamID   string
	Sequence   uint64
	ObservedAt time.Time
	ReceivedAt time.Time
}

type policyCapabilityDigest struct {
	PluginID     string   `json:"plugin_id"`
	Capabilities []string `json:"capabilities"`
}

func (store *Store) BuildNodePolicySnapshot(ctx context.Context, nodeID string, at time.Time) (NodePolicySnapshot, error) {
	if at.IsZero() {
		return NodePolicySnapshot{}, errors.New("policy evaluation time is required")
	}
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("begin policy snapshot: %w", err)
	}
	defer tx.Rollback()

	var enabled int
	var capabilitiesJSON []byte
	var agentStartedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT enabled, agent_capabilities_json, agent_started_at_ns
FROM nodes WHERE id = ?`, nodeID).Scan(&enabled, &capabilitiesJSON, &agentStartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodePolicySnapshot{}, ErrNotFound
	}
	if err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("read policy node: %w", err)
	}
	snapshot := NodePolicySnapshot{NodeID: nodeID, NodeEnabled: enabled != 0}
	if err := json.Unmarshal(capabilitiesJSON, &snapshot.AgentCapabilities); err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("decode policy Agent capabilities: %w", err)
	}
	if snapshot.AgentCapabilities == nil {
		snapshot.AgentCapabilities = []string{}
	}
	if agentStartedAt.Valid {
		startedAt := time.Unix(0, agentStartedAt.Int64).UTC()
		snapshot.AgentStartedAt = &startedAt
	}

	rows, err := tx.QueryContext(ctx, `
SELECT id, user_id, node_id, enabled, traffic_limit_bytes, reset_kind, reset_value, timezone,
       period_anchor, expires_at, soft_ip_limit, activity_window_seconds, block_duration_seconds,
       subscription_token_hash, created_at, updated_at
FROM authorizations WHERE node_id = ? ORDER BY id`, nodeID)
	if err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("read node authorizations for policy: %w", err)
	}
	authorizations := make([]Authorization, 0)
	for rows.Next() {
		value, scanErr := scanAuthorization(rows)
		if scanErr != nil {
			rows.Close()
			return NodePolicySnapshot{}, scanErr
		}
		authorizations = append(authorizations, value)
	}
	if err := rows.Close(); err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("close policy authorization rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("iterate policy authorizations: %w", err)
	}

	snapshot.Authorizations = make([]agentv1.AuthorizationPolicy, 0, len(authorizations))
	for _, authorization := range authorizations {
		policy, err := authorizationPolicy(ctx, tx, authorization, at.UTC())
		if err != nil {
			return NodePolicySnapshot{}, err
		}
		policy.Enabled = policy.Enabled && snapshot.NodeEnabled
		snapshot.Authorizations = append(snapshot.Authorizations, policy)
	}
	agentv1.SortPolicies(snapshot.Authorizations)

	plugins, err := policyPluginCapabilities(ctx, tx, nodeID)
	if err != nil {
		return NodePolicySnapshot{}, err
	}
	digestInput := struct {
		AgentCapabilities []string                      `json:"agent_capabilities"`
		Plugins           []policyCapabilityDigest      `json:"plugins"`
		Authorizations    []agentv1.AuthorizationPolicy `json:"authorizations"`
	}{snapshot.AgentCapabilities, plugins, snapshot.Authorizations}
	raw, err := json.Marshal(digestInput)
	if err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("encode policy snapshot digest: %w", err)
	}
	digest := sha256.Sum256(raw)
	snapshot.SHA256 = hex.EncodeToString(digest[:])
	if err := tx.Commit(); err != nil {
		return NodePolicySnapshot{}, fmt.Errorf("commit policy snapshot read: %w", err)
	}
	return snapshot, nil
}

func authorizationPolicy(ctx context.Context, tx *sql.Tx, value Authorization, at time.Time) (agentv1.AuthorizationPolicy, error) {
	reset := policyv1.ResetRule{Kind: value.ResetKind, Timezone: value.Timezone, PeriodAnchor: value.PeriodAnchor}
	if value.ResetValue != nil {
		converted := uint32(*value.ResetValue)
		reset.Value = &converted
	}
	period, err := policyv1.CurrentPeriod(reset, value.CreatedAt, at)
	if err != nil {
		return agentv1.AuthorizationPolicy{}, fmt.Errorf("compute authorization %s policy period: %w", value.ID, err)
	}
	policy := agentv1.AuthorizationPolicy{
		AuthorizationID: value.ID, StartedAt: value.CreatedAt, Enabled: value.Enabled,
		Reset: reset, CurrentPeriod: period, ExpiresAt: value.ExpiresAt,
		ActivityWindowSeconds: uint32(value.ActivityWindowSeconds),
		BlockDurationSeconds:  uint32(value.BlockDurationSeconds),
		Bindings:              make([]agentv1.ServiceBinding, 0),
	}
	if value.TrafficLimitBytes != nil {
		converted := uint64(*value.TrafficLimitBytes)
		policy.TrafficLimitBytes = &converted
	}
	if value.SoftIPLimit != nil {
		converted := uint32(*value.SoftIPLimit)
		policy.SoftIPLimit = &converted
	}
	rows, err := tx.QueryContext(ctx, `
	SELECT bindings.plugin_id, bindings.service_id
	FROM service_bindings bindings
	JOIN plugin_services services
	  ON services.node_id = ? AND services.plugin_id = bindings.plugin_id
	 AND services.service_id = bindings.service_id
	WHERE bindings.authorization_id = ? AND bindings.enabled = 1 AND services.enabled = 1
	ORDER BY bindings.plugin_id, bindings.service_id`, value.NodeID, value.ID)
	if err != nil {
		return agentv1.AuthorizationPolicy{}, fmt.Errorf("read authorization %s policy bindings: %w", value.ID, err)
	}
	for rows.Next() {
		var binding agentv1.ServiceBinding
		if err := rows.Scan(&binding.PluginID, &binding.ServiceID); err != nil {
			rows.Close()
			return agentv1.AuthorizationPolicy{}, fmt.Errorf("scan authorization policy binding: %w", err)
		}
		policy.Bindings = append(policy.Bindings, binding)
	}
	if err := rows.Close(); err != nil {
		return agentv1.AuthorizationPolicy{}, fmt.Errorf("close authorization policy bindings: %w", err)
	}
	if err := rows.Err(); err != nil {
		return agentv1.AuthorizationPolicy{}, fmt.Errorf("iterate authorization policy bindings: %w", err)
	}
	if err := agentv1.ValidateAuthorizationPolicy(policy); err != nil {
		return agentv1.AuthorizationPolicy{}, fmt.Errorf("validate authorization %s policy: %w", value.ID, err)
	}
	return policy, nil
}

func policyPluginCapabilities(ctx context.Context, tx *sql.Tx, nodeID string) ([]policyCapabilityDigest, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT plugin_id, capabilities_json FROM node_plugin_instances WHERE node_id = ? ORDER BY plugin_id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("read policy plugin capabilities: %w", err)
	}
	defer rows.Close()
	values := make([]policyCapabilityDigest, 0)
	for rows.Next() {
		var value policyCapabilityDigest
		var raw []byte
		if err := rows.Scan(&value.PluginID, &raw); err != nil {
			return nil, fmt.Errorf("scan policy plugin capabilities: %w", err)
		}
		if err := json.Unmarshal(raw, &value.Capabilities); err != nil {
			return nil, fmt.Errorf("decode policy plugin capabilities: %w", err)
		}
		if value.Capabilities == nil {
			value.Capabilities = []string{}
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate policy plugin capabilities: %w", err)
	}
	return values, nil
}

func (store *Store) StagePolicyReconcile(ctx context.Context, snapshot NodePolicySnapshot, commandID string,
	now time.Time, lifetime time.Duration,
) (AgentCommand, bool, error) {
	if snapshot.NodeID == "" || len(snapshot.SHA256) != 64 || lifetime <= 0 {
		return AgentCommand{}, false, errors.New("invalid policy reconcile staging input")
	}
	if err := protocol.ValidateIdempotencyKey(commandID); err != nil {
		return AgentCommand{}, false, fmt.Errorf("validate policy reconcile command ID: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("begin policy reconciliation: %w", err)
	}
	defer tx.Rollback()

	state, exists, err := nodePolicyStateTx(ctx, tx, snapshot.NodeID)
	if err != nil {
		return AgentCommand{}, false, err
	}
	payloadChanged := !exists || state.DesiredSHA256 != snapshot.SHA256
	sessionChanged := snapshot.AgentStartedAt != nil && (state.IssuedAgentStartedAt == nil ||
		!state.IssuedAgentStartedAt.Equal(*snapshot.AgentStartedAt))
	var pendingID string
	var pendingAttempts int
	var pendingExpiresAt int64
	err = tx.QueryRowContext(ctx, `
SELECT id, attempts, expires_at FROM agent_commands
WHERE node_id = ? AND kind = ? AND status = 'pending'`,
		snapshot.NodeID, agentv1.CommandPolicyReconcile).Scan(&pendingID, &pendingAttempts, &pendingExpiresAt)
	if err == nil {
		if !payloadChanged && sessionChanged {
			if _, err := tx.ExecContext(ctx, `
UPDATE node_policy_state SET issued_agent_started_at_ns = ?, updated_at = ? WHERE node_id = ?`,
				snapshot.AgentStartedAt.UTC().UnixNano(), unixTime(now), snapshot.NodeID); err != nil {
				return AgentCommand{}, false, fmt.Errorf("bind pending policy reconciliation to Agent session: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return AgentCommand{}, false, fmt.Errorf("commit policy Agent session binding: %w", err)
			}
			return AgentCommand{}, false, nil
		}
		replaceable := pendingAttempts == 0 && (pendingExpiresAt <= unixTime(now) || payloadChanged)
		if !replaceable {
			return AgentCommand{}, false, nil
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE id = ? AND status = 'pending' AND attempts = 0`, unixTime(now), unixTime(now), pendingID); err != nil {
			return AgentCommand{}, false, fmt.Errorf("expire stale policy reconciliation: %w", err)
		}
		err = sql.ErrNoRows
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AgentCommand{}, false, fmt.Errorf("check pending policy reconciliation: %w", err)
	}

	retryDue := state.ReconcileStatus == "failed" && state.RetryAfter != nil && !state.RetryAfter.After(now)
	if exists && !payloadChanged {
		if state.ReconcileStatus == "failed" && state.RetryAfter == nil {
			return AgentCommand{}, false, nil
		}
		if state.ReconcileStatus != "unsupported" && !sessionChanged && !retryDue {
			return AgentCommand{}, false, nil
		}
	}

	generation := state.DesiredGeneration
	if payloadChanged || generation == 0 {
		if generation >= math.MaxInt64 {
			return AgentCommand{}, false, errors.New("policy generation is exhausted")
		}
		generation++
	}
	reconcile := agentv1.PolicyReconcileCommand{Generation: generation, Authorizations: snapshot.Authorizations}
	command, err := agentv1.NewPolicyReconcileCommand(reconcile, now, now.Add(lifetime))
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("create policy reconcile command: %w", err)
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("encode policy reconcile command: %w", err)
	}
	digest, err := agentv1.CommandDigest(command)
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("digest policy reconcile command: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO agent_commands(id, node_id, kind, request_json, request_sha256, request_encrypted, scope_key,
    expires_at, created_at, updated_at)
SELECT ?, ?, ?, ?, ?, 0, '', ?, ?, ?
WHERE EXISTS (
    SELECT 1 FROM nodes WHERE id = ? AND credential_hash IS NOT NULL
)
ON CONFLICT DO NOTHING`, commandID, snapshot.NodeID, command.Kind, string(raw), digest,
		unixTime(command.ExpiresAt), unixTime(now), unixTime(now), snapshot.NodeID)
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("insert policy reconcile command: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("read policy reconcile command insertion: %w", err)
	}
	if inserted != 1 {
		return AgentCommand{}, false, ErrStateConflict
	}
	issuedAt := any(nil)
	if snapshot.AgentStartedAt != nil {
		issuedAt = snapshot.AgentStartedAt.UTC().UnixNano()
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO node_policy_state(
    node_id, desired_generation, desired_sha256, applied_generation, reconcile_status,
    last_problem_json, last_command_id, issued_agent_started_at_ns, retry_after, updated_at
) VALUES (?, ?, ?, 0, 'pending', NULL, ?, ?, NULL, ?)
ON CONFLICT(node_id) DO UPDATE SET
    desired_generation = excluded.desired_generation,
    desired_sha256 = excluded.desired_sha256,
    reconcile_status = 'pending',
    last_problem_json = NULL,
    last_command_id = excluded.last_command_id,
    issued_agent_started_at_ns = excluded.issued_agent_started_at_ns,
    retry_after = NULL,
    updated_at = excluded.updated_at`, snapshot.NodeID, int64(generation), snapshot.SHA256,
		commandID, issuedAt, unixTime(now))
	if err != nil {
		return AgentCommand{}, false, fmt.Errorf("store policy reconcile state: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "system", ActorID: "policy_coordinator",
		Action: "node.policy_reconcile.request", TargetType: "node", TargetID: snapshot.NodeID,
		Outcome: "success", Metadata: map[string]any{"command_id": commandID, "generation": generation},
	}); err != nil {
		return AgentCommand{}, false, err
	}
	if err := commit(tx, "policy reconciliation"); err != nil {
		return AgentCommand{}, false, err
	}
	created, err := store.AgentCommandByID(ctx, commandID)
	return created, true, err
}

func (store *Store) MarkPolicyUnsupported(ctx context.Context, nodeID, message string, now time.Time) error {
	problem := protocol.Problem{Code: protocol.ErrorUnsupported, Message: message, Retryable: false}
	raw, err := json.Marshal(problem)
	if err != nil {
		return fmt.Errorf("encode unsupported policy state: %w", err)
	}
	_, err = store.db.ExecContext(ctx, `
INSERT INTO node_policy_state(node_id, reconcile_status, last_problem_json, updated_at)
VALUES (?, 'unsupported', ?, ?)
ON CONFLICT(node_id) DO UPDATE SET reconcile_status = 'unsupported', last_problem_json = ?,
    retry_after = NULL, updated_at = ?`, nodeID, string(raw), unixTime(now), string(raw), unixTime(now))
	if err != nil {
		return fmt.Errorf("record unsupported policy state: %w", err)
	}
	return nil
}

func (store *Store) NodePolicyStateByID(ctx context.Context, nodeID string) (NodePolicyState, error) {
	return scanNodePolicyState(store.db.QueryRowContext(ctx, nodePolicyStateSelect+" WHERE node_id = ?", nodeID))
}

func (store *Store) ListNodePolicyStates(ctx context.Context) ([]NodePolicyState, error) {
	rows, err := store.db.QueryContext(ctx, nodePolicyStateSelect+" ORDER BY node_id")
	if err != nil {
		return nil, fmt.Errorf("list node policy states: %w", err)
	}
	defer rows.Close()
	values := make([]NodePolicyState, 0)
	for rows.Next() {
		value, err := scanNodePolicyState(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node policy states: %w", err)
	}
	return values, nil
}

const nodePolicyStateSelect = `
SELECT node_id, desired_generation, desired_sha256, applied_generation, reconcile_status,
       last_problem_json, last_command_id, issued_agent_started_at_ns, retry_after, updated_at
FROM node_policy_state`

func nodePolicyStateTx(ctx context.Context, tx *sql.Tx, nodeID string) (NodePolicyState, bool, error) {
	value, err := scanNodePolicyState(tx.QueryRowContext(ctx, nodePolicyStateSelect+" WHERE node_id = ?", nodeID))
	if errors.Is(err, ErrNotFound) {
		return NodePolicyState{NodeID: nodeID}, false, nil
	}
	return value, err == nil, err
}

func scanNodePolicyState(row rowScanner) (NodePolicyState, error) {
	var value NodePolicyState
	var desiredGeneration, appliedGeneration int64
	var problem, lastCommand sql.NullString
	var issuedAtNS, retryAfter sql.NullInt64
	var updatedAt int64
	if err := row.Scan(&value.NodeID, &desiredGeneration, &value.DesiredSHA256, &appliedGeneration,
		&value.ReconcileStatus, &problem, &lastCommand, &issuedAtNS, &retryAfter, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodePolicyState{}, ErrNotFound
		}
		return NodePolicyState{}, fmt.Errorf("scan node policy state: %w", err)
	}
	value.DesiredGeneration = uint64(desiredGeneration)
	value.AppliedGeneration = uint64(appliedGeneration)
	value.LastCommandID = lastCommand.String
	if problem.Valid {
		var decoded protocol.Problem
		if err := json.Unmarshal([]byte(problem.String), &decoded); err != nil {
			return NodePolicyState{}, fmt.Errorf("decode node policy problem: %w", err)
		}
		value.LastProblem = &decoded
	}
	if issuedAtNS.Valid {
		issuedAt := time.Unix(0, issuedAtNS.Int64).UTC()
		value.IssuedAgentStartedAt = &issuedAt
	}
	value.RetryAfter = nullableTime(retryAfter)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) ApplyTrafficSnapshot(ctx context.Context, source TrafficSnapshotSource,
	value agentv1.TrafficSnapshotEvent,
) error {
	if err := agentv1.ValidateTrafficSnapshotEvent(value); err != nil {
		return fmt.Errorf("validate traffic snapshot: %w", err)
	}
	if source.NodeID == "" || source.StreamID == "" || source.Sequence == 0 ||
		source.Sequence > agentv1.MaximumEventSequence ||
		source.ObservedAt.IsZero() || source.ReceivedAt.IsZero() {
		return errors.New("traffic snapshot source is invalid")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin traffic snapshot: %w", err)
	}
	defer tx.Rollback()
	var expectedNodeID string
	err = tx.QueryRowContext(ctx, "SELECT node_id FROM authorizations WHERE id = ?", value.AuthorizationID).Scan(&expectedNodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read traffic authorization: %w", err)
	}
	if expectedNodeID != source.NodeID {
		return ErrConflict
	}
	var revision, uploadBytes, downloadBytes, sourceSequence, observedAtNS int64
	var sourceStreamID string
	err = tx.QueryRowContext(ctx, `
SELECT revision, upload_bytes, download_bytes, source_stream_id, source_sequence,
       COALESCE(observed_at_ns, 0)
FROM traffic_periods
WHERE authorization_id = ? AND period_id = ?`, value.AuthorizationID, value.Period.ID).Scan(
		&revision, &uploadBytes, &downloadBytes, &sourceStreamID, &sourceSequence, &observedAtNS)
	if err == nil {
		sameSnapshot := uint64(revision) == value.Revision && uint64(uploadBytes) == value.UploadBytes &&
			uint64(downloadBytes) == value.DownloadBytes
		switch {
		case sourceStreamID == source.StreamID && uint64(sourceSequence) > source.Sequence:
			return tx.Commit()
		case sourceStreamID == source.StreamID && uint64(sourceSequence) == source.Sequence:
			if !sameSnapshot {
				return ErrConflict
			}
			return tx.Commit()
		case sourceStreamID != source.StreamID && observedAtNS > source.ObservedAt.UTC().UnixNano():
			return tx.Commit()
		case sourceStreamID != source.StreamID && observedAtNS == source.ObservedAt.UTC().UnixNano():
			if !sameSnapshot {
				return ErrConflict
			}
			return tx.Commit()
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read traffic period: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO traffic_periods(
    authorization_id, period_id, starts_at, ends_at, upload_bytes, download_bytes,
    revision, observed_at_ns, source_stream_id, source_sequence, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(authorization_id, period_id) DO UPDATE SET
    starts_at = excluded.starts_at, ends_at = excluded.ends_at,
    upload_bytes = excluded.upload_bytes, download_bytes = excluded.download_bytes,
	revision = excluded.revision, observed_at_ns = excluded.observed_at_ns,
	source_stream_id = excluded.source_stream_id, source_sequence = excluded.source_sequence,
	updated_at = excluded.updated_at`, value.AuthorizationID, value.Period.ID,
		unixTime(value.Period.StartsAt), nullableUnix(value.Period.EndsAt), int64(value.UploadBytes), int64(value.DownloadBytes),
		int64(value.Revision), source.ObservedAt.UTC().UnixNano(), source.StreamID, int64(source.Sequence),
		unixTime(source.ReceivedAt))
	if err != nil {
		return fmt.Errorf("store traffic snapshot: %w", err)
	}
	return commit(tx, "traffic snapshot")
}

func (store *Store) TrafficPeriods(ctx context.Context, authorizationID string, limit int) ([]TrafficPeriod, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("traffic period limit must be between 1 and 1000")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT authorization_id, period_id, starts_at, ends_at, upload_bytes, download_bytes,
       revision, enforced_at, coalesce(observed_at_ns, updated_at * 1000000000), updated_at
FROM traffic_periods WHERE authorization_id = ? ORDER BY starts_at DESC, period_id LIMIT ?`, authorizationID, limit)
	if err != nil {
		return nil, fmt.Errorf("list traffic periods: %w", err)
	}
	defer rows.Close()
	values := make([]TrafficPeriod, 0)
	for rows.Next() {
		value, err := scanTrafficPeriod(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic periods: %w", err)
	}
	return values, nil
}

func (store *Store) TrafficPeriodByID(ctx context.Context, authorizationID, periodID string) (TrafficPeriod, error) {
	value, err := scanTrafficPeriod(store.db.QueryRowContext(ctx, `
SELECT authorization_id, period_id, starts_at, ends_at, upload_bytes, download_bytes,
       revision, enforced_at, coalesce(observed_at_ns, updated_at * 1000000000), updated_at
FROM traffic_periods WHERE authorization_id = ? AND period_id = ?`, authorizationID, periodID))
	if errors.Is(err, sql.ErrNoRows) {
		return TrafficPeriod{}, ErrNotFound
	}
	return value, err
}

func (store *Store) ListCurrentTrafficPeriods(ctx context.Context) ([]TrafficPeriod, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT traffic.authorization_id, traffic.period_id, traffic.starts_at, traffic.ends_at,
       traffic.upload_bytes, traffic.download_bytes, traffic.revision, traffic.enforced_at,
       coalesce(traffic.observed_at_ns, traffic.updated_at * 1000000000), traffic.updated_at
FROM traffic_periods traffic
JOIN authorization_policy_status status
  ON status.authorization_id = traffic.authorization_id AND status.period_id = traffic.period_id
ORDER BY traffic.authorization_id`)
	if err != nil {
		return nil, fmt.Errorf("list current traffic periods: %w", err)
	}
	defer rows.Close()
	values := make([]TrafficPeriod, 0)
	for rows.Next() {
		value, err := scanTrafficPeriod(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current traffic periods: %w", err)
	}
	return values, nil
}

func (store *Store) ListLatestTrafficPeriods(ctx context.Context) ([]TrafficPeriod, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT authorization_id, period_id, starts_at, ends_at, upload_bytes, download_bytes,
       revision, enforced_at, coalesce(observed_at_ns, updated_at * 1000000000), updated_at
FROM traffic_periods current
WHERE NOT EXISTS (
    SELECT 1 FROM traffic_periods newer
    WHERE newer.authorization_id = current.authorization_id
      AND (newer.starts_at > current.starts_at OR
           (newer.starts_at = current.starts_at AND newer.period_id > current.period_id))
)
ORDER BY authorization_id`)
	if err != nil {
		return nil, fmt.Errorf("list latest traffic periods: %w", err)
	}
	defer rows.Close()
	values := make([]TrafficPeriod, 0)
	for rows.Next() {
		value, err := scanTrafficPeriod(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest traffic periods: %w", err)
	}
	return values, nil
}

func scanTrafficPeriod(row rowScanner) (TrafficPeriod, error) {
	var value TrafficPeriod
	var startsAt, uploadBytes, downloadBytes, revision, observedAtNS, updatedAt int64
	var endsAt, enforcedAt sql.NullInt64
	if err := row.Scan(&value.AuthorizationID, &value.Period.ID, &startsAt, &endsAt, &uploadBytes,
		&downloadBytes, &revision, &enforcedAt, &observedAtNS, &updatedAt); err != nil {
		return TrafficPeriod{}, fmt.Errorf("scan traffic period: %w", err)
	}
	value.Period.StartsAt = fromUnix(startsAt)
	value.Period.EndsAt = nullableTime(endsAt)
	value.UploadBytes = uint64(uploadBytes)
	value.DownloadBytes = uint64(downloadBytes)
	value.Revision = uint64(revision)
	value.EnforcedAt = nullableTime(enforcedAt)
	value.ObservedAt = time.Unix(0, observedAtNS).UTC()
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) RecordAuthorizationPolicyStatus(ctx context.Context, nodeID string, value agentv1.PolicyStatusEvent,
	observedAt, receivedAt time.Time,
) error {
	if err := agentv1.ValidatePolicyStatusEvent(value); err != nil {
		return fmt.Errorf("validate authorization policy status: %w", err)
	}
	var expectedNodeID string
	if err := store.db.QueryRowContext(ctx, "SELECT node_id FROM authorizations WHERE id = ?", value.AuthorizationID).Scan(&expectedNodeID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("read policy status authorization: %w", err)
	}
	if expectedNodeID != nodeID {
		return ErrConflict
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO authorization_policy_status(
    authorization_id, generation, period_id, starts_at, ends_at, upload_bytes, download_bytes,
    services_enabled, reason, active_ip_count, blocked_ip_count, observed_at_ns, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(authorization_id) DO UPDATE SET
    generation = excluded.generation, period_id = excluded.period_id, starts_at = excluded.starts_at,
    ends_at = excluded.ends_at, upload_bytes = excluded.upload_bytes,
    download_bytes = excluded.download_bytes, services_enabled = excluded.services_enabled,
    reason = excluded.reason, active_ip_count = excluded.active_ip_count,
    blocked_ip_count = excluded.blocked_ip_count, observed_at_ns = excluded.observed_at_ns,
    updated_at = excluded.updated_at
WHERE excluded.generation > authorization_policy_status.generation OR
      (excluded.generation = authorization_policy_status.generation AND
       excluded.observed_at_ns >= authorization_policy_status.observed_at_ns)`,
		value.AuthorizationID, int64(value.Generation), value.Period.ID, unixTime(value.Period.StartsAt),
		nullableUnix(value.Period.EndsAt), int64(value.UploadBytes), int64(value.DownloadBytes),
		boolInt(value.ServicesEnabled), value.Reason, int64(value.ActiveIPCount), int64(value.BlockedIPCount),
		observedAt.UTC().UnixNano(), unixTime(receivedAt))
	if err != nil {
		return fmt.Errorf("store authorization policy status: %w", err)
	}
	return nil
}

func (store *Store) AuthorizationPolicyStatusByID(ctx context.Context, authorizationID string) (AuthorizationPolicyStatus, error) {
	return scanAuthorizationPolicyStatus(store.db.QueryRowContext(ctx, `
SELECT authorization_id, generation, period_id, starts_at, ends_at, upload_bytes, download_bytes,
       services_enabled, reason, active_ip_count, blocked_ip_count, observed_at_ns, updated_at
FROM authorization_policy_status WHERE authorization_id = ?`, authorizationID))
}

func (store *Store) ListAuthorizationPolicyStatuses(ctx context.Context) ([]AuthorizationPolicyStatus, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT authorization_id, generation, period_id, starts_at, ends_at, upload_bytes, download_bytes,
       services_enabled, reason, active_ip_count, blocked_ip_count, observed_at_ns, updated_at
FROM authorization_policy_status ORDER BY authorization_id`)
	if err != nil {
		return nil, fmt.Errorf("list authorization policy statuses: %w", err)
	}
	defer rows.Close()
	values := make([]AuthorizationPolicyStatus, 0)
	for rows.Next() {
		value, err := scanAuthorizationPolicyStatus(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorization policy statuses: %w", err)
	}
	return values, nil
}

func scanAuthorizationPolicyStatus(row rowScanner) (AuthorizationPolicyStatus, error) {
	var value AuthorizationPolicyStatus
	var generation, startsAt, uploadBytes, downloadBytes, activeIPCount, blockedIPCount int64
	var endsAt sql.NullInt64
	var servicesEnabled int
	var observedAtNS, updatedAt int64
	err := row.Scan(
		&value.AuthorizationID, &generation, &value.Period.ID, &startsAt, &endsAt, &uploadBytes,
		&downloadBytes, &servicesEnabled, &value.Reason, &activeIPCount, &blockedIPCount,
		&observedAtNS, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthorizationPolicyStatus{}, ErrNotFound
	}
	if err != nil {
		return AuthorizationPolicyStatus{}, fmt.Errorf("read authorization policy status: %w", err)
	}
	value.Generation = uint64(generation)
	value.Period.StartsAt = fromUnix(startsAt)
	value.Period.EndsAt = nullableTime(endsAt)
	value.UploadBytes = uint64(uploadBytes)
	value.DownloadBytes = uint64(downloadBytes)
	value.ServicesEnabled = servicesEnabled != 0
	value.ActiveIPCount = uint32(activeIPCount)
	value.BlockedIPCount = uint32(blockedIPCount)
	value.ObservedAt = time.Unix(0, observedAtNS).UTC()
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) AccessEventReferenceKnown(ctx context.Context, nodeID string, value agentv1.AccessEvent) (bool, error) {
	var authorizationNodeID string
	err := store.db.QueryRowContext(ctx, "SELECT node_id FROM authorizations WHERE id = ?", value.AuthorizationID).Scan(&authorizationNodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read access event authorization: %w", err)
	}
	if authorizationNodeID != nodeID {
		return false, ErrConflict
	}
	var exists int
	err = store.db.QueryRowContext(ctx, `
SELECT 1 FROM service_bindings
WHERE authorization_id = ? AND plugin_id = ? AND service_id = ? AND enabled = 1`,
		value.AuthorizationID, value.PluginID, value.ServiceID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read access event service binding: %w", err)
	}
	return true, nil
}
