package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/manifest"
)

func TestNodePluginDesiredCommandCompletionAndStatus(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 13, 0, 0, 0, time.UTC)
	credential := make([]byte, 32)
	credential[0] = 1
	preparePluginStore(t, database, credential, now)

	configuration := json.RawMessage(`{"token":"must-not-leak","enabled":true}`)
	digest, err := agentv1.PluginConfigurationDigest(configuration)
	if err != nil {
		t.Fatalf("PluginConfigurationDigest() error = %v", err)
	}
	request := testPluginCommand(t, 1, agentv1.PluginStateRunning, configuration, now)
	created, err := database.ApplyNodePluginDesired(ctx, NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 1,
		DesiredState: agentv1.PluginStateRunning, DesiredVersion: "1.2.3",
		DesiredConfigurationSHA256: digest, ArtifactSize: 1234, ArtifactSHA256: strings.Repeat("b", 64),
	}, []byte("encrypted configuration"), "plugin-command-1", request, []byte("encrypted command"), now)
	if err != nil {
		t.Fatalf("ApplyNodePluginDesired() error = %v", err)
	}
	if created.Generation != 1 || created.ActualState != agentv1.PluginStateAbsent ||
		created.ReconcileStatus != AgentCommandPending || created.CommandStatus != AgentCommandPending {
		t.Fatalf("created node plugin = %+v", created)
	}
	storedCommand, err := database.AgentCommandByID(ctx, "plugin-command-1")
	if err != nil || !storedCommand.RequestEncrypted || storedCommand.Request.Kind != "" || storedCommand.ScopeKey != "io.relayward.test" {
		t.Fatalf("stored encrypted command = %+v, %v", storedCommand, err)
	}
	var metadata string
	if err := database.db.QueryRowContext(ctx, `SELECT request_json FROM agent_commands WHERE id = 'plugin-command-1'`).Scan(&metadata); err != nil {
		t.Fatalf("read command metadata: %v", err)
	}
	if strings.Contains(metadata, "must-not-leak") || strings.Contains(metadata, "releases/download") {
		t.Fatalf("command metadata contains a secret or download URL: %s", metadata)
	}
	if secret, err := database.Secret(ctx, AgentCommandSecretOwnerType, "plugin-command-1", AgentCommandRequestSecret); err != nil || string(secret) != "encrypted command" {
		t.Fatalf("command secret = %q, %v", secret, err)
	}
	ownerID := NodePluginSecretOwnerID("node-id", "io.relayward.test")
	if secret, err := database.Secret(ctx, NodePluginSecretOwnerType, ownerID, NodePluginConfigurationSecret); err != nil || string(secret) != "encrypted configuration" {
		t.Fatalf("configuration secret = %q, %v", secret, err)
	}

	conflicting := testPluginCommand(t, 2, agentv1.PluginStateStopped, configuration, now.Add(time.Minute))
	if _, err := database.ApplyNodePluginDesired(ctx, NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 2,
		DesiredState: agentv1.PluginStateStopped, DesiredVersion: "1.2.3",
		DesiredConfigurationSHA256: digest, ArtifactSize: 1234, ArtifactSHA256: strings.Repeat("b", 64),
	}, []byte("new configuration"), "plugin-command-2", conflicting, []byte("new command"), now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyNodePluginDesired() pending conflict error = %v", err)
	}
	unchanged, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || unchanged.Generation != 1 || unchanged.DesiredState != agentv1.PluginStateRunning {
		t.Fatalf("node plugin after rolled back conflict = %+v, %v", unchanged, err)
	}

	requestDigest, _ := agentv1.CommandDigest(request)
	wrongOutput, _ := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: "io.relayward.test", Generation: 1, State: agentv1.PluginStateStopped,
		Version: "1.2.3", ConfigurationSHA256: digest,
	})
	result := agentv1.CommandResult{
		CommandID: "plugin-command-1", RequestSHA256: requestDigest, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: now.Add(2 * time.Minute), Output: wrongOutput,
	}
	if err := database.CompleteEncryptedAgentCommand(ctx, "node-id", credential, result, request, now.Add(2*time.Minute)); err == nil {
		t.Fatal("CompleteEncryptedAgentCommand() accepted mismatched output")
	}
	validOutput, _ := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: "io.relayward.test", Generation: 1, State: agentv1.PluginStateRunning,
		Version: "1.2.3", ConfigurationSHA256: digest,
	})
	result.Output = validOutput
	newerStatus := agentv1.PluginStatusEvent{
		PluginID: "io.relayward.test", Generation: 1, State: agentv1.PluginStateFailed,
		Version: "1.2.3", ConfigurationSHA256: digest, Health: agentv1.PluginHealthUnhealthy,
		Reason: "health checks failed before result delivery", RestartCount: 2,
	}
	if err := database.RecordNodePluginStatus(ctx, "node-id", newerStatus,
		result.CompletedAt.Add(time.Second), result.CompletedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("RecordNodePluginStatus() before command result error = %v", err)
	}
	if err := database.CompleteEncryptedAgentCommand(ctx, "node-id", credential, result, request, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteEncryptedAgentCommand() error = %v", err)
	}
	if err := database.CompleteAgentCommand(ctx, "node-id", credential, result, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("CompleteAgentCommand() terminal replay error = %v", err)
	}
	if _, err := database.Secret(ctx, AgentCommandSecretOwnerType, "plugin-command-1", AgentCommandRequestSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed command secret error = %v", err)
	}
	actual, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || actual.ActualState != agentv1.PluginStateFailed || actual.Health != agentv1.PluginHealthUnhealthy ||
		actual.ActualGeneration != 1 || actual.ActualConfigurationSHA256 != digest || actual.ReconcileStatus != AgentCommandSucceeded ||
		actual.RestartCount != 2 || actual.Reason != newerStatus.Reason {
		t.Fatalf("reconciled node plugin = %+v, %v", actual, err)
	}

	statusObservedAt := result.CompletedAt.Add(2 * time.Second)
	status := agentv1.PluginStatusEvent{
		PluginID: "io.relayward.test", Generation: 1, State: agentv1.PluginStateFailed,
		Version: "1.2.3", ConfigurationSHA256: digest, Health: agentv1.PluginHealthUnhealthy,
		Reason: "health checks failed", RestartCount: 3,
	}
	if err := database.RecordNodePluginStatus(ctx, "node-id", status, statusObservedAt, statusObservedAt.Add(time.Second)); err != nil {
		t.Fatalf("RecordNodePluginStatus() error = %v", err)
	}
	failed, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || failed.ActualState != agentv1.PluginStateFailed || failed.RestartCount != 3 || failed.Reason != status.Reason {
		t.Fatalf("failed plugin status = %+v, %v", failed, err)
	}
	status.State = agentv1.PluginStateRunning
	status.Health = agentv1.PluginHealthHealthy
	status.Reason = ""
	status.RestartCount = 1
	if err := database.RecordNodePluginStatus(ctx, "node-id", status, statusObservedAt.Add(-time.Second), statusObservedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("RecordNodePluginStatus() stale error = %v", err)
	}
	stillFailed, _ := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if stillFailed.ActualState != agentv1.PluginStateFailed || stillFailed.RestartCount != 3 {
		t.Fatalf("stale status overwrote current state: %+v", stillFailed)
	}

	absentAt := now.Add(4 * time.Minute)
	absentRequest := testPluginCommand(t, 2, agentv1.PluginStateAbsent, nil, absentAt)
	absent, err := database.ApplyNodePluginDesired(ctx, NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 2, DesiredState: agentv1.PluginStateAbsent,
	}, nil, "plugin-command-absent", absentRequest, []byte("encrypted absent command"), absentAt)
	if err != nil || absent.Generation != 2 {
		t.Fatalf("ApplyNodePluginDesired() absent = %+v, %v", absent, err)
	}
	if _, err := database.Secret(ctx, NodePluginSecretOwnerType, ownerID, NodePluginConfigurationSecret); err != nil {
		t.Fatalf("configuration was removed before absent confirmation: %v", err)
	}
	oldGenerationStatus := agentv1.PluginStatusEvent{
		PluginID: "io.relayward.test", Generation: 1, State: agentv1.PluginStateRunning,
		Version: "1.2.3", ConfigurationSHA256: digest, Health: agentv1.PluginHealthHealthy, RestartCount: 4,
	}
	if err := database.RecordNodePluginStatus(ctx, "node-id", oldGenerationStatus, absentAt.Add(30*time.Second), absentAt.Add(31*time.Second)); err != nil {
		t.Fatalf("RecordNodePluginStatus() actual generation during pending error = %v", err)
	}
	duringAbsent, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || duringAbsent.Generation != 2 || duringAbsent.ActualGeneration != 1 ||
		duringAbsent.ActualState != agentv1.PluginStateRunning || duringAbsent.RestartCount != 4 {
		t.Fatalf("actual generation status during pending = %+v, %v", duringAbsent, err)
	}
	absentDigest, _ := agentv1.CommandDigest(absentRequest)
	absentOutput, _ := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: "io.relayward.test", Generation: 2, State: agentv1.PluginStateAbsent,
	})
	if err := database.CompleteEncryptedAgentCommand(ctx, "node-id", credential, agentv1.CommandResult{
		CommandID: "plugin-command-absent", RequestSHA256: absentDigest, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: absentAt.Add(time.Minute), Output: absentOutput,
	}, absentRequest, absentAt.Add(time.Minute)); err != nil {
		t.Fatalf("CompleteEncryptedAgentCommand() absent error = %v", err)
	}
	if _, err := database.Secret(ctx, NodePluginSecretOwnerType, ownerID, NodePluginConfigurationSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("confirmed absent configuration secret error = %v", err)
	}

	if err := database.DeleteNode(ctx, "node-id", now.Add(6*time.Minute)); err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if _, err := database.Secret(ctx, NodePluginSecretOwnerType, ownerID, NodePluginConfigurationSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted node configuration secret error = %v", err)
	}
}

func TestExpiredPluginCommandReleasesScopeAndDeletesCiphertext(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 13, 0, 0, 0, time.UTC)
	preparePluginStore(t, database, make([]byte, 32), now)
	configuration := json.RawMessage(`{"enabled":true}`)
	digest, _ := agentv1.PluginConfigurationDigest(configuration)
	first := testPluginCommand(t, 1, agentv1.PluginStateRunning, configuration, now)
	desired := NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 1,
		DesiredState: agentv1.PluginStateRunning, DesiredVersion: "1.2.3",
		DesiredConfigurationSHA256: digest, ArtifactSize: 1234, ArtifactSHA256: strings.Repeat("b", 64),
	}
	if _, err := database.ApplyNodePluginDesired(ctx, desired, []byte("config-1"), "plugin-command-1", first, []byte("command-1"), now); err != nil {
		t.Fatalf("ApplyNodePluginDesired() first error = %v", err)
	}
	replacementAt := now.Add(31 * time.Minute)
	if err := database.ExpireNodePluginCommands(ctx, replacementAt); err != nil {
		t.Fatalf("ExpireNodePluginCommands() error = %v", err)
	}
	expiredInstance, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || expiredInstance.ReconcileStatus != AgentCommandExpired || expiredInstance.CommandStatus != AgentCommandExpired {
		t.Fatalf("expired node plugin = %+v, %v", expiredInstance, err)
	}
	desired.Generation = 2
	second := testPluginCommand(t, 2, agentv1.PluginStateRunning, configuration, replacementAt)
	if _, err := database.ApplyNodePluginDesired(ctx, desired, []byte("config-2"), "plugin-command-2", second, []byte("command-2"), replacementAt); err != nil {
		t.Fatalf("ApplyNodePluginDesired() replacement error = %v", err)
	}
	expired, err := database.AgentCommandByID(ctx, "plugin-command-1")
	if err != nil || expired.Status != AgentCommandExpired {
		t.Fatalf("expired plugin command = %+v, %v", expired, err)
	}
	if _, err := database.Secret(ctx, AgentCommandSecretOwnerType, "plugin-command-1", AgentCommandRequestSecret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired command secret error = %v", err)
	}
}

func TestDeliveredPluginCommandDoesNotExpireBeforeItsResult(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 13, 0, 0, 0, time.UTC)
	preparePluginStore(t, database, make([]byte, 32), now)
	configuration := json.RawMessage(`{"enabled":true}`)
	digest, _ := agentv1.PluginConfigurationDigest(configuration)
	request := testPluginCommand(t, 1, agentv1.PluginStateRunning, configuration, now)
	if _, err := database.ApplyNodePluginDesired(ctx, NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 1,
		DesiredState: agentv1.PluginStateRunning, DesiredVersion: "1.2.3",
		DesiredConfigurationSHA256: digest, ArtifactSize: 1234, ArtifactSHA256: strings.Repeat("b", 64),
	}, []byte("configuration"), "plugin-command-delivered", request, []byte("command"), now); err != nil {
		t.Fatalf("ApplyNodePluginDesired() error = %v", err)
	}
	if err := database.MarkAgentCommandSent(ctx, "plugin-command-delivered", "node-id", now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkAgentCommandSent() error = %v", err)
	}
	if err := database.ExpireNodePluginCommands(ctx, now.Add(31*time.Minute)); err != nil {
		t.Fatalf("ExpireNodePluginCommands() error = %v", err)
	}
	command, err := database.AgentCommandByID(ctx, "plugin-command-delivered")
	if err != nil || command.Status != AgentCommandPending || command.Attempts != 1 {
		t.Fatalf("delivered plugin command after expiry = %+v, %v", command, err)
	}
	if _, err := database.Secret(ctx, AgentCommandSecretOwnerType, command.ID, AgentCommandRequestSecret); err != nil {
		t.Fatalf("delivered plugin command ciphertext error = %v", err)
	}
}

func TestPluginInstallationRejectsRepositoryCredentials(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	pluginManifest := testRuntimeManifest()
	err = database.CreatePluginInstallation(t.Context(), PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://token@github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("CreatePluginInstallation() accepted repository credentials")
	}
	if _, readErr := database.PluginInstallationByID(t.Context(), pluginManifest.ID); !errors.Is(readErr, ErrNotFound) {
		t.Fatalf("PluginInstallationByID() after rejection error = %v", readErr)
	}
}

func TestDiscardUnrecoverableSecretsPreservesRuntimeStateAndRequiresConfiguration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 19, 0, 0, 0, time.UTC)
	if _, err := database.InitializeAdministrator(ctx, "admin", "password-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := database.EnableTOTP(ctx, []byte("encrypted-totp"), [][]byte{make([]byte, 32)}, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, Session{
		TokenHash: make([]byte, 32), CSRFHash: make([]byte, 32), AdministratorID: 1,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	credential := make([]byte, 32)
	credential[0] = 1
	preparePluginStore(t, database, credential, now)
	if err := database.PutSecret(ctx, PluginInstallationSecretOwnerType, "io.relayward.test", PluginInstallationGitHubToken, []byte("encrypted-token"), now); err != nil {
		t.Fatal(err)
	}
	configuration := json.RawMessage(`{"enabled":true}`)
	digest, err := agentv1.PluginConfigurationDigest(configuration)
	if err != nil {
		t.Fatal(err)
	}
	command := testPluginCommand(t, 1, agentv1.PluginStateRunning, configuration, now)
	if _, err := database.ApplyNodePluginDesired(ctx, NodePluginDesired{
		NodeID: "node-id", PluginID: "io.relayward.test", Generation: 1,
		DesiredState: agentv1.PluginStateRunning, DesiredVersion: "1.2.3",
		DesiredConfigurationSHA256: digest, ArtifactSize: 1234, ArtifactSHA256: strings.Repeat("b", 64),
	}, []byte("encrypted-configuration"), "recovery-command", command, []byte("encrypted-command"), now); err != nil {
		t.Fatal(err)
	}

	result, err := database.DiscardUnrecoverableSecrets(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.DiscardedSecrets != 4 || result.ExpiredCommands != 1 || result.PluginsRequiringConfiguration != 1 {
		t.Fatalf("recovery result = %+v", result)
	}
	if count, err := database.CountSecrets(ctx); err != nil || count != 0 {
		t.Fatalf("secret count = %d, %v", count, err)
	}
	administrator, err := database.AdministratorByID(ctx, 1)
	if err != nil || administrator.TOTPEnabled {
		t.Fatalf("administrator after recovery = %+v, %v", administrator, err)
	}
	var sessions int
	if err := database.db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("session count = %d, %v", sessions, err)
	}
	storedCommand, err := database.AgentCommandByID(ctx, "recovery-command")
	if err != nil || storedCommand.Status != AgentCommandExpired {
		t.Fatalf("command after recovery = %+v, %v", storedCommand, err)
	}
	instance, err := database.NodePluginInstanceByID(ctx, "node-id", "io.relayward.test")
	if err != nil || instance.DesiredState != agentv1.PluginStateRunning || instance.DesiredConfigurationSHA256 != "" ||
		instance.ReconcileStatus != AgentCommandFailed || instance.LastProblem == nil {
		t.Fatalf("plugin instance after recovery = %+v, %v", instance, err)
	}
	audit, err := database.ListAudit(ctx, 0, 1)
	if err != nil || len(audit) != 1 || audit[0].Action != "system.secrets.recover" {
		t.Fatalf("recovery audit = %+v, %v", audit, err)
	}
}

func preparePluginStore(t *testing.T, database *Store, credential []byte, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET credential_hash = ?, registered_at = ? WHERE id = 'node-id'`, credential, unixTime(now)); err != nil {
		t.Fatalf("register node: %v", err)
	}
	pluginManifest := testRuntimeManifest()
	if err := database.CreatePluginInstallation(ctx, PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
}

func testRuntimeManifest() manifest.Manifest {
	agentAPI := uint32(1)
	return manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.test", Name: "Test Runtime",
		Version: "1.2.3", Kind: manifest.KindRuntime,
		Requires:    manifest.Requirements{ControlAPI: 1, AgentAPI: &agentAPI},
		Permissions: []manifest.Permission{},
		Artifacts: []manifest.Artifact{
			{Role: manifest.ArtifactCenter, File: "center", Size: 1234, SHA256: strings.Repeat("a", 64), OS: "linux", Arch: "amd64"},
			{Role: manifest.ArtifactNode, File: "node", Size: 1234, SHA256: strings.Repeat("b", 64), OS: "linux", Arch: "amd64"},
		},
	}
}

func testPluginCommand(t *testing.T, generation uint64, state string, configuration json.RawMessage, issuedAt time.Time) agentv1.Command {
	t.Helper()
	request := agentv1.PluginReconcileCommand{PluginID: "io.relayward.test", Generation: generation, DesiredState: state}
	if state != agentv1.PluginStateAbsent {
		request.Version = "1.2.3"
		request.Configuration = configuration
		request.Artifact = &agentv1.PluginArtifact{
			DownloadURL: "https://github.com/Relayward/test-plugin/releases/download/v1.2.3/node",
			Size:        1234, SHA256: strings.Repeat("b", 64),
		}
	}
	command, err := agentv1.NewPluginReconcileCommand(request, issuedAt, issuedAt.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("NewPluginReconcileCommand() error = %v", err)
	}
	return command
}
