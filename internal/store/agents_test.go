package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAgentRegistrationAuthenticationAndReplacement(t *testing.T) {
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

	firstToken := make([]byte, 32)
	firstCredential := make([]byte, 32)
	firstCredential[0] = 1
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", firstToken, now.Add(time.Minute), now); err != nil {
		t.Fatalf("CreateNodeRegistrationToken() error = %v", err)
	}
	registered, err := database.RegisterAgent(ctx, AgentRegistration{
		TokenHash: firstToken, CredentialHash: firstCredential, AgentVersion: "0.1.0",
		Hostname: "edge-one", OS: "linux", Arch: "amd64", Capabilities: []string{"control.heartbeat"},
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if registered.Hostname != "edge-one" || registered.AgentVersion != "0.1.0" || registered.RegisteredAt == nil {
		t.Fatalf("registered node = %+v", registered)
	}
	if _, err := database.RegisterAgent(ctx, AgentRegistration{TokenHash: firstToken, CredentialHash: firstCredential}, now.Add(2*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused RegisterAgent() error = %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, "node-id", firstCredential); err != nil {
		t.Fatalf("AuthenticateAgent() first credential error = %v", err)
	}

	secondToken := make([]byte, 32)
	secondToken[0] = 2
	secondCredential := make([]byte, 32)
	secondCredential[0] = 3
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", secondToken, now.Add(2*time.Minute), now.Add(3*time.Second)); err != nil {
		t.Fatalf("second CreateNodeRegistrationToken() error = %v", err)
	}
	if _, err := database.RegisterAgent(ctx, AgentRegistration{
		TokenHash: secondToken, CredentialHash: secondCredential, AgentVersion: "0.2.0",
		Hostname: "edge-two", OS: "linux", Arch: "amd64",
	}, now.Add(4*time.Second)); err != nil {
		t.Fatalf("second RegisterAgent() error = %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, "node-id", firstCredential); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthenticateAgent() replaced credential error = %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, "node-id", secondCredential); err != nil {
		t.Fatalf("AuthenticateAgent() second credential error = %v", err)
	}
	if err := database.RecordAgentHeartbeat(ctx, "node-id", firstCredential, "0.2.0", now.Add(5*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RecordAgentHeartbeat() replaced credential error = %v", err)
	}
	startedAt := now.Add(-time.Minute)
	if err := database.RecordAgentHello(ctx, "node-id", secondCredential, "0.2.1", []string{"control.heartbeat", "event.queue"}, startedAt, now.Add(6*time.Second)); err != nil {
		t.Fatalf("RecordAgentHello() error = %v", err)
	}
	if err := database.RecordAgentHeartbeat(ctx, "node-id", secondCredential, "0.2.1", now.Add(7*time.Second)); err != nil {
		t.Fatalf("RecordAgentHeartbeat() error = %v", err)
	}
	current, err := database.NodeByID(ctx, "node-id")
	if err != nil || current.LastSeenAt == nil || current.AgentStartedAt == nil || !current.AgentStartedAt.Equal(startedAt) ||
		current.AgentVersion != "0.2.1" || len(current.Capabilities) != 2 {
		t.Fatalf("node after hello and heartbeat = %+v, error = %v", current, err)
	}
	current.Enabled = false
	if err := database.UpdateNode(ctx, current, now.Add(8*time.Second)); err != nil {
		t.Fatalf("disable registered node: %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, "node-id", secondCredential); err != nil {
		t.Fatalf("AuthenticateAgent() disabled node credential error = %v", err)
	}
	if err := database.RecordAgentHeartbeat(ctx, "node-id", secondCredential, "0.2.1", now.Add(9*time.Second)); err != nil {
		t.Fatalf("RecordAgentHeartbeat() disabled node error = %v", err)
	}
}

func TestAgentCredentialRevocationAndReregistration(t *testing.T) {
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

	register := func(tokenMarker, credentialMarker byte, observedAt time.Time) []byte {
		t.Helper()
		token := make([]byte, 32)
		token[0] = tokenMarker
		credential := make([]byte, 32)
		credential[0] = credentialMarker
		if err := database.CreateNodeRegistrationToken(ctx, "node-id", token, observedAt.Add(time.Minute), observedAt); err != nil {
			t.Fatalf("CreateNodeRegistrationToken() error = %v", err)
		}
		if _, err := database.RegisterAgent(ctx, AgentRegistration{
			TokenHash: token, CredentialHash: credential, AgentVersion: "0.1.0",
			Hostname: "edge", OS: "linux", Arch: "amd64", Capabilities: []string{"control.heartbeat"},
		}, observedAt.Add(time.Second)); err != nil {
			t.Fatalf("RegisterAgent() error = %v", err)
		}
		return credential
	}

	firstCredential := register(1, 2, now.Add(time.Second))
	secondCredential := register(3, 4, now.Add(3*time.Second))
	if _, err := database.AuthenticateAgent(ctx, "node-id", firstCredential); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthenticateAgent() rotated credential error = %v", err)
	}

	pendingToken := make([]byte, 32)
	pendingToken[0] = 5
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", pendingToken, now.Add(time.Hour), now.Add(5*time.Second)); err != nil {
		t.Fatalf("CreateNodeRegistrationToken() pending error = %v", err)
	}
	if err := database.RevokeNodeCredential(ctx, "node-id", now.Add(6*time.Second)); err != nil {
		t.Fatalf("RevokeNodeCredential() error = %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, "node-id", secondCredential); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AuthenticateAgent() revoked credential error = %v", err)
	}
	current, err := database.NodeByID(ctx, "node-id")
	if err != nil {
		t.Fatalf("NodeByID() after revocation error = %v", err)
	}
	if current.CredentialHash != nil || current.RegisteredAt != nil || current.LastSeenAt != nil ||
		current.Hostname != "" || current.AgentVersion != "" || current.AgentOS != "" || current.AgentArch != "" ||
		len(current.Capabilities) != 0 || current.AgentStartedAt != nil {
		t.Fatalf("node after credential revocation = %+v", current)
	}
	var pendingUsedAt sql.NullInt64
	if err := database.db.QueryRowContext(ctx, `SELECT used_at FROM node_registration_tokens WHERE token_hash = ?`, pendingToken).Scan(&pendingUsedAt); err != nil {
		t.Fatalf("read pending registration token: %v", err)
	}
	if !pendingUsedAt.Valid {
		t.Fatal("credential revocation left a registration token active")
	}
	if err := database.RevokeNodeCredential(ctx, "node-id", now.Add(7*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("second RevokeNodeCredential() error = %v", err)
	}

	thirdCredential := register(6, 7, now.Add(8*time.Second))
	if _, err := database.AuthenticateAgent(ctx, "node-id", thirdCredential); err != nil {
		t.Fatalf("AuthenticateAgent() reregistered credential error = %v", err)
	}
	entries, err := database.ListAudit(ctx, 0, 30)
	if err != nil {
		t.Fatalf("ListAudit() error = %v", err)
	}
	for _, action := range []string{"node.register", "node.credential.rotate", "node.credential.revoke", "node.reregister"} {
		if findAuditAction(entries, action) == nil {
			t.Fatalf("audit action %q not found in %+v", action, entries)
		}
	}
}

func TestDisabledNodeDoesNotConsumeRegistrationToken(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 8, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: false}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	token := make([]byte, 32)
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", token, now.Add(time.Minute), now); err != nil {
		t.Fatalf("CreateNodeRegistrationToken() error = %v", err)
	}
	if _, err := database.RegisterAgent(ctx, AgentRegistration{
		TokenHash: token, CredentialHash: make([]byte, 32), AgentVersion: "0.1.0",
		Hostname: "edge", OS: "linux", Arch: "amd64",
	}, now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RegisterAgent() disabled node error = %v", err)
	}
	var usedAt any
	if err := database.db.QueryRowContext(ctx, "SELECT used_at FROM node_registration_tokens WHERE token_hash = ?", token).Scan(&usedAt); err != nil {
		t.Fatalf("read registration token: %v", err)
	}
	if usedAt != nil {
		t.Fatalf("disabled node registration consumed token at %v", usedAt)
	}
}

func TestAgentRegistrationTokenIsConsumedOnceConcurrently(t *testing.T) {
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
	token := make([]byte, 32)
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", token, now.Add(time.Minute), now); err != nil {
		t.Fatalf("CreateNodeRegistrationToken() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index := byte(1); index <= 2; index++ {
		workers.Add(1)
		go func(marker byte) {
			defer workers.Done()
			<-start
			credential := make([]byte, 32)
			credential[0] = marker
			_, err := database.RegisterAgent(ctx, AgentRegistration{
				TokenHash: token, CredentialHash: credential, AgentVersion: "0.1.0",
				Hostname: "edge", OS: "linux", Arch: "amd64",
			}, now.Add(time.Second))
			results <- err
		}(index)
	}
	close(start)
	workers.Wait()
	close(results)

	succeeded := 0
	rejected := 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrNotFound):
			rejected++
		default:
			t.Fatalf("RegisterAgent() concurrent error = %v", result)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent registration results: succeeded = %d, rejected = %d", succeeded, rejected)
	}
}
