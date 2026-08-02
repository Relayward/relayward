package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/store"
)

func TestNodePluginManagementAPIQueuesEncryptedState(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	csrfHeaders := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	node := createPluginNode(t, handler, sessionCookie, csrfCookie)
	identity := registerPluginAgent(t, handler, node.ID, sessionCookie, csrfCookie)
	pluginManifest := serverRuntimeManifest()
	now := time.Now().UTC()
	if err := database.CreatePluginInstallation(context.Background(), store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
	path := "/api/v1/nodes/" + node.ID + "/plugins/" + pluginManifest.ID
	body := []byte(`{"desired_state":"running","version":"1.2.3","configuration":{"credential":"must-not-leak"}}`)
	unauthenticated := performRequest(handler, http.MethodPut, path, body, csrfHeaders)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated plugin update status = %d", unauthenticated.Code)
	}
	withoutCSRF := performRequest(handler, http.MethodPut, path, body, map[string]string{"Content-Type": "application/json"}, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("plugin update without CSRF status = %d", withoutCSRF.Code)
	}
	response := performRequest(handler, http.MethodPut, path, body, csrfHeaders, sessionCookie)
	if response.Code != http.StatusOK {
		t.Fatalf("plugin update status = %d, body = %s", response.Code, response.Body.String())
	}
	var instance nodePluginResponse
	decodeResponse(t, response, &instance)
	if instance.NodeID != node.ID || instance.PluginID != pluginManifest.ID || instance.Generation != 1 ||
		instance.CommandStatus != store.AgentCommandPending || instance.DesiredConfigurationSHA256 == "" {
		t.Fatalf("plugin response = %+v", instance)
	}
	if strings.Contains(response.Body.String(), "must-not-leak") || strings.Contains(response.Body.String(), "download_url") {
		t.Fatalf("plugin response leaked configuration: %s", response.Body.String())
	}
	stored, err := database.AgentCommandByID(context.Background(), instance.LastCommandID)
	if err != nil || !stored.RequestEncrypted || stored.Request.Kind != "" {
		t.Fatalf("stored plugin command = %+v, %v", stored, err)
	}
	get := performRequest(handler, http.MethodGet, path, nil, nil, sessionCookie)
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), "must-not-leak") {
		t.Fatalf("get plugin status = %d, body = %s", get.Code, get.Body.String())
	}
	list := performRequest(handler, http.MethodGet, "/api/v1/node-plugin-instances", nil, nil, sessionCookie)
	if list.Code != http.StatusOK {
		t.Fatalf("list plugins status = %d, body = %s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []nodePluginResponse `json:"items"`
	}
	decodeResponse(t, list, &listed)
	if len(listed.Items) != 1 || listed.Items[0].PluginName != pluginManifest.Name {
		t.Fatalf("listed plugins = %+v", listed.Items)
	}
	conflict := performRequest(handler, http.MethodPut, path,
		[]byte(`{"desired_state":"stopped","version":"1.2.3","configuration":{"enabled":true}}`),
		csrfHeaders, sessionCookie)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("pending plugin conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
	if identity.NodeID != node.ID {
		t.Fatalf("registered Agent node = %q, want %q", identity.NodeID, node.ID)
	}
}

func TestNodePluginCommandDispatchAndCompletionOverWSS(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	node := createPluginNode(t, handler, sessionCookie, csrfCookie)
	identity := registerPluginAgent(t, handler, node.ID, sessionCookie, csrfCookie)
	pluginManifest := serverRuntimeManifest()
	if err := database.CreatePluginInstallation(t.Context(), store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, time.Now().UTC()); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
	path := "/api/v1/nodes/" + node.ID + "/plugins/" + pluginManifest.ID
	queued := performRequest(handler, http.MethodPut, path,
		[]byte(`{"desired_state":"running","version":"1.2.3","configuration":{"credential":"wss-secret"}}`),
		map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if queued.Code != http.StatusOK {
		t.Fatalf("queue plugin status = %d, body = %s", queued.Code, queued.Body.String())
	}
	var instance nodePluginResponse
	decodeResponse(t, queued, &instance)

	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	capabilities := []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityControlHeartbeat,
		agentv1.CapabilityEventQueue, agentv1.CapabilityPluginSupervision,
	}
	connection := connectTestAgentWithCapabilities(t, testServer.URL, identity, "0.1.0", capabilities)
	defer connection.Close()
	centerHello := readTestAgentEnvelope(t, connection)
	session, err := agentv1.DecodeEnvelopePayload[agentv1.CenterHello](centerHello)
	if err != nil {
		t.Fatalf("decode center hello: %v", err)
	}
	heartbeat, err := agentv1.NewEnvelope(agentv1.MessageAgentHeartbeat, agentv1.Heartbeat{
		SessionID: session.SessionID, AgentVersion: "0.1.0", ObservedAt: time.Now().UTC(),
	})
	if err != nil || connection.WriteJSON(heartbeat) != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	heartbeatAck := readTestAgentEnvelope(t, connection)
	ackPayload, err := agentv1.DecodeEnvelopePayload[agentv1.HeartbeatAck](heartbeatAck)
	if err != nil || ackPayload.Command == nil {
		t.Fatalf("heartbeat acknowledgement = %+v, %v", ackPayload, err)
	}
	command, err := agentv1.DecodeEnvelopePayload[agentv1.Command](*ackPayload.Command)
	if err != nil || ackPayload.Command.IdempotencyKey != instance.LastCommandID {
		t.Fatalf("dispatched plugin command = %+v, %v", ackPayload.Command, err)
	}
	reconcile, err := agentv1.DecodePluginReconcileCommand(command)
	if err != nil || !strings.Contains(string(reconcile.Configuration), "wss-secret") {
		t.Fatalf("dispatched plugin request = %+v, %v", reconcile, err)
	}
	digest, err := agentv1.CommandDigest(command)
	if err != nil {
		t.Fatalf("CommandDigest() error = %v", err)
	}
	output, err := agentv1.EncodePluginReconcileOutput(agentv1.PluginReconcileOutput{
		PluginID: reconcile.PluginID, Generation: reconcile.Generation, State: reconcile.DesiredState,
		Version: reconcile.Version, ConfigurationSHA256: instance.DesiredConfigurationSHA256,
	})
	if err != nil {
		t.Fatalf("EncodePluginReconcileOutput() error = %v", err)
	}
	resultEnvelope, err := agentv1.NewCommandResultEnvelope(agentv1.CommandResult{
		CommandID: instance.LastCommandID, RequestSHA256: digest, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: time.Now().UTC(), Output: output,
	})
	if err != nil || connection.WriteJSON(resultEnvelope) != nil {
		t.Fatalf("send plugin command result: %v", err)
	}
	resultAck := readTestAgentEnvelope(t, connection)
	if resultAck.Type != agentv1.MessageCenterCommandResultAck || resultAck.CorrelationID != resultEnvelope.ID {
		t.Fatalf("plugin result acknowledgement = %+v", resultAck)
	}
	completed := performRequest(handler, http.MethodGet, path, nil, nil, sessionCookie)
	if completed.Code != http.StatusOK {
		t.Fatalf("completed plugin status = %d, body = %s", completed.Code, completed.Body.String())
	}
	decodeResponse(t, completed, &instance)
	if instance.CommandStatus != store.AgentCommandSucceeded || instance.ActualState != agentv1.PluginStateRunning ||
		instance.ActualGeneration != instance.Generation {
		t.Fatalf("completed plugin instance = %+v", instance)
	}
}

func createPluginNode(t *testing.T, handler http.Handler, sessionCookie, csrfCookie *http.Cookie) nodeResponse {
	t.Helper()
	response := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Plugin edge"}`),
		map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if response.Code != http.StatusCreated {
		t.Fatalf("create plugin node status = %d, body = %s", response.Code, response.Body.String())
	}
	var node nodeResponse
	decodeResponse(t, response, &node)
	return node
}

func registerPluginAgent(t *testing.T, handler http.Handler, nodeID string, sessionCookie, csrfCookie *http.Cookie) agentv1.RegisterResponse {
	t.Helper()
	tokenResponse := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+nodeID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create plugin registration token status = %d, body = %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token struct {
		Value string `json:"token"`
	}
	decodeResponse(t, tokenResponse, &token)
	body, err := json.Marshal(agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: token.Value, AgentVersion: "0.1.0",
		Hostname: "plugin-edge", OS: "linux", Arch: "amd64", Capabilities: []string{
			agentv1.CapabilityControlCommands, agentv1.CapabilityControlHeartbeat,
			agentv1.CapabilityEventQueue, agentv1.CapabilityPluginSupervision,
		},
	})
	if err != nil {
		t.Fatalf("marshal plugin Agent registration: %v", err)
	}
	response := performRequest(handler, http.MethodPost, "/api/v1/agent/register", body,
		map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusCreated {
		t.Fatalf("register plugin Agent status = %d, body = %s", response.Code, response.Body.String())
	}
	var identity agentv1.RegisterResponse
	decodeResponse(t, response, &identity)
	return identity
}

func serverRuntimeManifest() manifest.Manifest {
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
