package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	policyv1 "github.com/Relayward/relayward-sdk/policy/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

const (
	testPolicyNodeID          = "10000000-0000-4000-8000-000000000001"
	testPolicyUserID          = "20000000-0000-4000-8000-000000000002"
	testPolicyAuthorizationID = "30000000-0000-4000-8000-000000000003"
)

func TestPolicySnapshotReconcileAndTrafficLifecycle(t *testing.T) {
	ctx := context.Background()
	database, credential, now := preparePolicyStore(t)
	defer database.Close()

	snapshot, err := database.BuildNodePolicySnapshot(ctx, testPolicyNodeID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BuildNodePolicySnapshot() error = %v", err)
	}
	if len(snapshot.Authorizations) != 1 || snapshot.Authorizations[0].AuthorizationID != testPolicyAuthorizationID ||
		snapshot.SHA256 == "" || snapshot.AgentStartedAt == nil {
		t.Fatalf("policy snapshot = %+v", snapshot)
	}
	command, created, err := database.StagePolicyReconcile(ctx, snapshot, "policy-command-1", now.Add(time.Minute), time.Hour)
	if err != nil || !created {
		t.Fatalf("StagePolicyReconcile() = %+v, %t, %v", command, created, err)
	}
	if duplicate, created, err := database.StagePolicyReconcile(ctx, snapshot, "policy-command-unused", now.Add(2*time.Minute), time.Hour); err != nil || created || duplicate.ID != "" {
		t.Fatalf("duplicate StagePolicyReconcile() = %+v, %t, %v", duplicate, created, err)
	}
	reconcile, err := agentv1.DecodePolicyReconcileCommand(command.Request)
	if err != nil || reconcile.Generation != 1 || len(reconcile.Authorizations) != 1 {
		t.Fatalf("policy reconcile command = %+v, %v", reconcile, err)
	}
	output, _ := agentv1.EncodePolicyReconcileOutput(agentv1.PolicyReconcileOutput{
		Generation: reconcile.Generation, AuthorizationCount: uint32(len(reconcile.Authorizations)),
	})
	result := agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: now.Add(3 * time.Minute), Output: output,
	}
	if err := database.CompleteAgentCommand(ctx, testPolicyNodeID, credential, result, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() policy error = %v", err)
	}
	state, err := database.NodePolicyStateByID(ctx, testPolicyNodeID)
	if err != nil || state.DesiredGeneration != 1 || state.AppliedGeneration != 1 || state.ReconcileStatus != "applied" {
		t.Fatalf("applied policy state = %+v, %v", state, err)
	}

	period := snapshot.Authorizations[0].CurrentPeriod
	traffic := agentv1.TrafficSnapshotEvent{
		AuthorizationID: testPolicyAuthorizationID, Period: period, Revision: 2,
		UploadBytes: 100, DownloadBytes: 200,
	}
	if err := database.ApplyTrafficSnapshot(ctx, testPolicyNodeID, traffic, now.Add(4*time.Minute), now.Add(4*time.Minute)); err != nil {
		t.Fatalf("ApplyTrafficSnapshot() error = %v", err)
	}
	if err := database.ApplyTrafficSnapshot(ctx, testPolicyNodeID, traffic, now.Add(5*time.Minute), now.Add(5*time.Minute)); err != nil {
		t.Fatalf("ApplyTrafficSnapshot() duplicate error = %v", err)
	}
	conflict := traffic
	conflict.UploadBytes++
	if err := database.ApplyTrafficSnapshot(ctx, testPolicyNodeID, conflict, now.Add(5*time.Minute), now.Add(5*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyTrafficSnapshot() conflict error = %v", err)
	}
	older := traffic
	older.Revision = 1
	older.UploadBytes = 1
	if err := database.ApplyTrafficSnapshot(ctx, testPolicyNodeID, older, now.Add(6*time.Minute), now.Add(6*time.Minute)); err != nil {
		t.Fatalf("ApplyTrafficSnapshot() older revision error = %v", err)
	}
	periods, err := database.TrafficPeriods(ctx, testPolicyAuthorizationID, 10)
	if err != nil || len(periods) != 1 || periods[0].Revision != 2 || periods[0].UploadBytes != 100 {
		t.Fatalf("TrafficPeriods() = %+v, %v", periods, err)
	}

	policyStatus := agentv1.PolicyStatusEvent{
		Generation: 1, AuthorizationID: testPolicyAuthorizationID, Period: period,
		UploadBytes: 100, DownloadBytes: 200, ServicesEnabled: true,
		Reason: agentv1.PolicyReasonActive, ActiveIPCount: 1,
	}
	if err := database.RecordAuthorizationPolicyStatus(ctx, testPolicyNodeID, policyStatus, now.Add(7*time.Minute), now.Add(7*time.Minute)); err != nil {
		t.Fatalf("RecordAuthorizationPolicyStatus() error = %v", err)
	}
	nextPeriod, err := policyv1.CurrentPeriod(
		policyv1.ResetRule{Kind: policyv1.ResetDaily, Timezone: "UTC"}, now, now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("next CurrentPeriod() error = %v", err)
	}
	if err := database.ApplyTrafficSnapshot(ctx, testPolicyNodeID, agentv1.TrafficSnapshotEvent{
		AuthorizationID: testPolicyAuthorizationID, Period: nextPeriod, Revision: 1,
		UploadBytes: 10, DownloadBytes: 20,
	}, now.Add(8*time.Minute), now.Add(8*time.Minute)); err != nil {
		t.Fatalf("ApplyTrafficSnapshot() next period error = %v", err)
	}
	latest, err := database.ListLatestTrafficPeriods(ctx)
	if err != nil || len(latest) != 1 || latest[0].Period.ID != nextPeriod.ID || latest[0].Revision != 1 {
		t.Fatalf("ListLatestTrafficPeriods() = %+v, %v", latest, err)
	}
	policyStatus.Generation = 2
	if err := database.RecordAuthorizationPolicyStatus(ctx, testPolicyNodeID, policyStatus, now.Add(9*time.Minute), now.Add(9*time.Minute)); err != nil {
		t.Fatalf("RecordAuthorizationPolicyStatus() changed period error = %v", err)
	}
	current, err := database.ListCurrentTrafficPeriods(ctx)
	if err != nil || len(current) != 1 || current[0].Period.ID != period.ID || current[0].Revision != 2 {
		t.Fatalf("ListCurrentTrafficPeriods() = %+v, %v", current, err)
	}
	currentByID, err := database.TrafficPeriodByID(ctx, testPolicyAuthorizationID, period.ID)
	if err != nil || currentByID.Period.ID != period.ID || currentByID.Revision != 2 {
		t.Fatalf("TrafficPeriodByID() = %+v, %v", currentByID, err)
	}
	statuses, err := database.ListAuthorizationPolicyStatuses(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].AuthorizationID != testPolicyAuthorizationID || !statuses[0].ServicesEnabled {
		t.Fatalf("ListAuthorizationPolicyStatuses() = %+v, %v", statuses, err)
	}
	queued, err := database.CreateAgentCommand(ctx, "queued-before-disable", testPolicyNodeID, agentv1.Command{
		Kind: "agent.test", IssuedAt: now.Add(8 * time.Minute), ExpiresAt: now.Add(time.Hour), Payload: json.RawMessage(`{}`),
	}, now.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("CreateAgentCommand() before disable error = %v", err)
	}
	node, err := database.NodeByID(ctx, testPolicyNodeID)
	if err != nil {
		t.Fatalf("NodeByID() before disable error = %v", err)
	}
	node.Enabled = false
	if err := database.UpdateNode(ctx, node, now.Add(9*time.Minute)); err != nil {
		t.Fatalf("UpdateNode() disable error = %v", err)
	}
	disabledSnapshot, err := database.BuildNodePolicySnapshot(ctx, testPolicyNodeID, now.Add(10*time.Minute))
	if err != nil || disabledSnapshot.NodeEnabled || len(disabledSnapshot.Authorizations) != 1 || disabledSnapshot.Authorizations[0].Enabled {
		t.Fatalf("disabled policy snapshot = %+v, %v", disabledSnapshot, err)
	}
	disableCommand, created, err := database.StagePolicyReconcile(ctx, disabledSnapshot, "policy-disable", now.Add(10*time.Minute), time.Hour)
	if err != nil || !created {
		t.Fatalf("disabled node StagePolicyReconcile() = %+v, %t, %v", disableCommand, created, err)
	}
	next, err := database.NextAgentCommand(ctx, testPolicyNodeID, now.Add(11*time.Minute))
	if err != nil || next.ID != disableCommand.ID {
		t.Fatalf("disabled node next command = %+v, %v", next, err)
	}
	disableRequest, _ := agentv1.DecodePolicyReconcileCommand(disableCommand.Request)
	disableOutput, _ := agentv1.EncodePolicyReconcileOutput(agentv1.PolicyReconcileOutput{
		Generation: disableRequest.Generation, AuthorizationCount: uint32(len(disableRequest.Authorizations)),
	})
	if err := database.CompleteAgentCommand(ctx, testPolicyNodeID, credential, agentv1.CommandResult{
		CommandID: disableCommand.ID, RequestSHA256: disableCommand.RequestSHA256,
		Status: agentv1.CommandStatusSucceeded, CompletedAt: now.Add(12 * time.Minute), Output: disableOutput,
	}, now.Add(12*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() disabled policy error = %v", err)
	}
	if _, err := database.NextAgentCommand(ctx, testPolicyNodeID, now.Add(13*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled node dispatched non-policy command: %v", err)
	}
	storedQueued, err := database.AgentCommandByID(ctx, queued.ID)
	if err != nil || storedQueued.Status != AgentCommandPending || storedQueued.Attempts != 0 {
		t.Fatalf("queued disabled-node command = %+v, %v", storedQueued, err)
	}
}

func TestPolicyRetryUsesSameGenerationAndNewAgentSessionReplays(t *testing.T) {
	ctx := context.Background()
	database, credential, now := preparePolicyStore(t)
	defer database.Close()
	snapshot, _ := database.BuildNodePolicySnapshot(ctx, testPolicyNodeID, now.Add(time.Minute))
	first, created, err := database.StagePolicyReconcile(ctx, snapshot, "policy-retry-1", now.Add(time.Minute), time.Hour)
	if err != nil || !created {
		t.Fatalf("first StagePolicyReconcile() = %+v, %t, %v", first, created, err)
	}
	failure := agentv1.CommandResult{
		CommandID: first.ID, RequestSHA256: first.RequestSHA256, Status: agentv1.CommandStatusFailed,
		CompletedAt: now.Add(2 * time.Minute),
		Problem:     &protocol.Problem{Code: protocol.ErrorUnavailable, Message: "runtime unavailable", Retryable: true},
	}
	if err := database.CompleteAgentCommand(ctx, testPolicyNodeID, credential, failure, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() retryable failure = %v", err)
	}
	if _, created, err := database.StagePolicyReconcile(ctx, snapshot, "policy-too-soon", now.Add(2*time.Minute+29*time.Second), time.Hour); err != nil || created {
		t.Fatalf("early policy retry created = %t, error = %v", created, err)
	}
	retry, created, err := database.StagePolicyReconcile(ctx, snapshot, "policy-retry-2", now.Add(2*time.Minute+31*time.Second), time.Hour)
	if err != nil || !created {
		t.Fatalf("policy retry = %+v, %t, %v", retry, created, err)
	}
	retryPayload, _ := agentv1.DecodePolicyReconcileCommand(retry.Request)
	if retryPayload.Generation != 1 {
		t.Fatalf("retry generation = %d, want 1", retryPayload.Generation)
	}
	retryOutput, _ := agentv1.EncodePolicyReconcileOutput(agentv1.PolicyReconcileOutput{Generation: 1, AuthorizationCount: 1})
	if err := database.CompleteAgentCommand(ctx, testPolicyNodeID, credential, agentv1.CommandResult{
		CommandID: retry.ID, RequestSHA256: retry.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: now.Add(4 * time.Minute), Output: retryOutput,
	}, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("complete policy retry: %v", err)
	}

	newStartedAt := now.Add(10 * time.Minute)
	if _, err := database.db.ExecContext(ctx, "UPDATE nodes SET agent_started_at_ns = ? WHERE id = ?", newStartedAt.UnixNano(), testPolicyNodeID); err != nil {
		t.Fatalf("change Agent session: %v", err)
	}
	replayedSnapshot, _ := database.BuildNodePolicySnapshot(ctx, testPolicyNodeID, now.Add(11*time.Minute))
	replay, created, err := database.StagePolicyReconcile(ctx, replayedSnapshot, "policy-replay", now.Add(11*time.Minute), time.Hour)
	if err != nil || !created {
		t.Fatalf("new session policy replay = %+v, %t, %v", replay, created, err)
	}
	replayPayload, _ := agentv1.DecodePolicyReconcileCommand(replay.Request)
	if replayPayload.Generation != 1 {
		t.Fatalf("new session replay generation = %d, want 1", replayPayload.Generation)
	}
}

func preparePolicyStore(t *testing.T) (*Store, []byte, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	credential := make([]byte, 32)
	credential[0] = 1
	if err := database.CreateNode(ctx, Node{ID: testPolicyNodeID, Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	capabilities := `["control.commands","event.queue","policy.enforcement"]`
	startedAt := now.Add(-time.Minute)
	if _, err := database.db.ExecContext(ctx, `
UPDATE nodes SET credential_hash = ?, registered_at = ?, agent_capabilities_json = ?, agent_started_at_ns = ?
WHERE id = ?`, credential, unixTime(now), capabilities, startedAt.UnixNano(), testPolicyNodeID); err != nil {
		t.Fatalf("prepare policy node: %v", err)
	}
	if err := database.CreateUser(ctx, User{ID: testPolicyUserID, DisplayName: "user"}, now); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := database.CreateAuthorization(ctx, Authorization{
		ID: testPolicyAuthorizationID, UserID: testPolicyUserID, NodeID: testPolicyNodeID, Enabled: true,
		ResetKind: "daily", Timezone: "UTC", ActivityWindowSeconds: 600,
		BlockDurationSeconds: 1800, SubscriptionTokenHash: make([]byte, 32),
	}, now); err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}
	return database, credential, now
}
