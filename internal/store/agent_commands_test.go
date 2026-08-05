package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

func TestAgentCommandLifecycleAndIdempotency(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 8, 0, 0, 0, time.UTC)
	credential := make([]byte, 32)
	credential[0] = 1
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE nodes SET credential_hash = ? WHERE id = ?", credential, "node-id"); err != nil {
		t.Fatalf("set credential: %v", err)
	}
	request := agentv1.Command{
		Kind: "agent.test", IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		Payload: json.RawMessage(`{"value":1}`),
	}
	created, err := database.CreateAgentCommand(ctx, "command-1", "node-id", request, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() error = %v", err)
	}
	duplicate, err := database.CreateAgentCommand(ctx, "command-1", "node-id", request, now.Add(time.Second))
	if err != nil || duplicate.RequestSHA256 != created.RequestSHA256 {
		t.Fatalf("duplicate CreateAgentCommand() = %+v, %v", duplicate, err)
	}
	changed := request
	changed.Payload = json.RawMessage(`{"value":2}`)
	if _, err := database.CreateAgentCommand(ctx, "command-1", "node-id", changed, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateAgentCommand() conflicting reuse error = %v", err)
	}
	next, err := database.NextAgentCommand(ctx, "node-id", now.Add(time.Minute))
	if err != nil || next.ID != "command-1" {
		t.Fatalf("NextAgentCommand() = %+v, %v", next, err)
	}
	if err := database.MarkAgentCommandSent(ctx, next.ID, next.NodeID, now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkAgentCommandSent() error = %v", err)
	}
	result := agentv1.CommandResult{
		CommandID: next.ID, RequestSHA256: next.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: now.Add(90 * time.Second), Output: json.RawMessage(`{"applied":true}`),
	}
	wrongDigest := result
	wrongDigest.RequestSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, wrongDigest, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompleteAgentCommand() digest error = %v", err)
	}
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() error = %v", err)
	}
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("duplicate CompleteAgentCommand() error = %v", err)
	}
	differentResult := result
	differentResult.Output = json.RawMessage(`{"version":"different"}`)
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, differentResult, now.Add(3*time.Minute)); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("CompleteAgentCommand() conflicting result error = %v", err)
	}
	stored, err := database.AgentCommandByID(ctx, "command-1")
	if err != nil || stored.Status != AgentCommandSucceeded || stored.Attempts != 1 || stored.Result == nil ||
		stored.CompletedAt == nil || !stored.CompletedAt.Equal(now.Add(2*time.Minute)) || !stored.Result.CompletedAt.Equal(now.Add(90*time.Second)) {
		t.Fatalf("stored Agent command = %+v, %v", stored, err)
	}
	if _, err := database.NextAgentCommand(ctx, "node-id", now.Add(4*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NextAgentCommand() after completion error = %v", err)
	}
}

func TestAgentCommandExpiryAndCredentialFence(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 8, 0, 0, 0, time.UTC)
	credential := make([]byte, 32)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE nodes SET credential_hash = ? WHERE id = ?", credential, "node-id"); err != nil {
		t.Fatalf("set credential: %v", err)
	}
	request := agentv1.Command{Kind: "agent.test", IssuedAt: now, ExpiresAt: now.Add(time.Minute), Payload: json.RawMessage(`{}`)}
	command, err := database.CreateAgentCommand(ctx, "command-expired", "node-id", request, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() error = %v", err)
	}
	if _, err := database.NextAgentCommand(ctx, "node-id", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NextAgentCommand() expired error = %v", err)
	}
	stored, err := database.AgentCommandByID(ctx, command.ID)
	if err != nil || stored.Status != AgentCommandExpired {
		t.Fatalf("expired command = %+v, %v", stored, err)
	}
	result := agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256, Status: agentv1.CommandStatusFailed,
		CompletedAt: now.Add(2 * time.Minute),
		Problem:     &protocol.Problem{Code: protocol.ErrorUnavailable, Message: "expired", Retryable: true},
	}
	wrongCredential := make([]byte, 32)
	wrongCredential[0] = 1
	if err := database.CompleteAgentCommand(ctx, "node-id", wrongCredential, result, now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CompleteAgentCommand() credential fence error = %v", err)
	}
}

func TestAgentCommandsDispatchInOrderAndSentCommandExpires(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 8, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	first := agentv1.Command{Kind: "agent.first", IssuedAt: now, ExpiresAt: now.Add(time.Minute), Payload: json.RawMessage(`{}`)}
	second := agentv1.Command{Kind: "agent.second", IssuedAt: now, ExpiresAt: now.Add(time.Hour), Payload: json.RawMessage(`{}`)}
	if _, err := database.CreateAgentCommand(ctx, "command-z", "node-id", first, now); err != nil {
		t.Fatalf("create first command: %v", err)
	}
	if _, err := database.CreateAgentCommand(ctx, "command-a", "node-id", second, now); err != nil {
		t.Fatalf("create second command: %v", err)
	}
	dispatched, err := database.NextAgentCommand(ctx, "node-id", now.Add(10*time.Second))
	if err != nil || dispatched.ID != "command-z" {
		t.Fatalf("first dispatch = %+v, %v", dispatched, err)
	}
	if err := database.MarkAgentCommandSent(ctx, dispatched.ID, dispatched.NodeID, now.Add(10*time.Second)); err != nil {
		t.Fatalf("mark first command sent: %v", err)
	}
	redelivered, err := database.NextAgentCommand(ctx, "node-id", now.Add(20*time.Second))
	if err != nil || redelivered.ID != "command-z" {
		t.Fatalf("redelivery = %+v, %v", redelivered, err)
	}
	next, err := database.NextAgentCommand(ctx, "node-id", now.Add(2*time.Minute))
	if err != nil || next.ID != "command-a" {
		t.Fatalf("dispatch after expiry = %+v, %v", next, err)
	}
	expired, err := database.AgentCommandByID(ctx, "command-z")
	if err != nil || expired.Status != AgentCommandExpired {
		t.Fatalf("sent command after expiry = %+v, %v", expired, err)
	}
}

func TestAgentCommandSurvivesStoreRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relayward.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	now := time.Now().UTC()
	if err := first.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	created, err := first.CreateAgentCommand(ctx, "command-restart", "node-id", agentv1.Command{
		Kind: "agent.test", IssuedAt: now, ExpiresAt: now.Add(time.Hour), Payload: json.RawMessage(`{}`),
	}, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() second error = %v", err)
	}
	defer second.Close()
	dispatched, err := second.NextAgentCommand(ctx, "node-id", now.Add(time.Minute))
	if err != nil || dispatched.ID != created.ID || dispatched.RequestSHA256 != created.RequestSHA256 {
		t.Fatalf("command after restart = %+v, %v", dispatched, err)
	}
}

func TestAgentUpdateAllowsOnePendingCommandAndExpiresIt(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	firstRequest, err := agentv1.NewAgentUpdateCommand("0.2.0", now, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("NewAgentUpdateCommand() error = %v", err)
	}
	first, err := database.CreateAgentCommand(ctx, "update-1", "node-id", firstRequest, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() first error = %v", err)
	}
	duplicate, err := database.CreateAgentCommand(ctx, first.ID, "node-id", firstRequest, now.Add(time.Minute))
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate CreateAgentCommand() = %+v, %v", duplicate, err)
	}
	secondRequest, err := agentv1.NewAgentUpdateCommand("0.3.0", now.Add(time.Minute), now.Add(31*time.Minute))
	if err != nil {
		t.Fatalf("NewAgentUpdateCommand() second error = %v", err)
	}
	if _, err := database.CreateAgentCommand(ctx, "update-2", "node-id", secondRequest, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second pending CreateAgentCommand() error = %v", err)
	}
	latest, err := database.LatestAgentCommandByKind(ctx, "node-id", agentv1.CommandAgentUpdate, now.Add(2*time.Minute))
	if err != nil || latest.ID != first.ID || latest.Status != AgentCommandPending {
		t.Fatalf("LatestAgentCommandByKind() = %+v, %v", latest, err)
	}

	replacementRequest, err := agentv1.NewAgentUpdateCommand("0.3.0", now.Add(30*time.Minute), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewAgentUpdateCommand() replacement error = %v", err)
	}
	replacement, err := database.CreateAgentCommand(ctx, "update-3", "node-id", replacementRequest, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("CreateAgentCommand() after expiry error = %v", err)
	}
	expired, err := database.AgentCommandByID(ctx, first.ID)
	if err != nil || expired.Status != AgentCommandExpired {
		t.Fatalf("expired Agent update = %+v, %v", expired, err)
	}
	latest, err = database.LatestAgentCommandByKind(ctx, "node-id", agentv1.CommandAgentUpdate, now.Add(31*time.Minute))
	if err != nil || latest.ID != replacement.ID || latest.Status != AgentCommandPending {
		t.Fatalf("latest replacement = %+v, %v", latest, err)
	}
}

func TestAgentUpdateCompletionRequiresMatchingActivatedOutput(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	credential := make([]byte, 32)
	credential[0] = 1
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE nodes SET credential_hash = ? WHERE id = ?", credential, "node-id"); err != nil {
		t.Fatalf("set credential: %v", err)
	}
	request, err := agentv1.NewAgentUpdateCommand("0.2.0", now, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("NewAgentUpdateCommand() error = %v", err)
	}
	command, err := database.CreateAgentCommand(ctx, "update-1", "node-id", request, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() error = %v", err)
	}

	invalidOutputs := []json.RawMessage{
		json.RawMessage(`{"version":"0.2.0"}`),
		json.RawMessage(`{"version":"0.3.0","state":"activated"}`),
		json.RawMessage(`{"version":"0.2.0","state":"activated","unexpected":true}`),
	}
	for _, output := range invalidOutputs {
		result := agentv1.CommandResult{
			CommandID: command.ID, RequestSHA256: command.RequestSHA256,
			Status: agentv1.CommandStatusSucceeded, CompletedAt: now.Add(time.Minute), Output: output,
		}
		if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(time.Minute)); err == nil {
			t.Fatalf("CompleteAgentCommand() accepted output %s", output)
		}
		stored, err := database.AgentCommandByID(ctx, command.ID)
		if err != nil || stored.Status != AgentCommandPending || stored.Result != nil || stored.CompletedAt != nil {
			t.Fatalf("command after rejected output %s = %+v, %v", output, stored, err)
		}
	}

	output, err := agentv1.EncodeAgentUpdateOutput(agentv1.AgentUpdateOutput{
		Version: "0.2.0", State: agentv1.AgentUpdateStateActivated,
	})
	if err != nil {
		t.Fatalf("EncodeAgentUpdateOutput() error = %v", err)
	}
	result := agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256,
		Status: agentv1.CommandStatusSucceeded, CompletedAt: now.Add(2 * time.Minute), Output: output,
	}
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() valid error = %v", err)
	}
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() replay error = %v", err)
	}

	entries, err := database.ListAudit(ctx, 0, 20)
	if err != nil {
		t.Fatalf("ListAudit() error = %v", err)
	}
	requestAudit := findAuditAction(entries, "node.agent_update.request")
	completeAudit := findAuditAction(entries, "node.agent_update.complete")
	if requestAudit == nil || requestAudit.TargetType != "node" || requestAudit.TargetID != "node-id" ||
		requestAudit.Metadata["command_id"] != command.ID || requestAudit.Metadata["version"] != "0.2.0" {
		t.Fatalf("request audit = %+v", requestAudit)
	}
	if completeAudit == nil || completeAudit.ActorType != "agent" || completeAudit.Outcome != "success" ||
		completeAudit.TargetID != "node-id" || completeAudit.Metadata["command_id"] != command.ID || completeAudit.Metadata["version"] != "0.2.0" {
		t.Fatalf("completion audit = %+v", completeAudit)
	}
}

func findAuditAction(entries []AuditEntry, action string) *AuditEntry {
	for index := range entries {
		if entries[index].Action == action {
			return &entries[index]
		}
	}
	return nil
}
