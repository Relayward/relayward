package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/auth"
)

func TestAgentUpdateManagementAPI(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	csrfHeaders := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}

	create := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`), csrfHeaders, sessionCookie)
	if create.Code != http.StatusCreated {
		t.Fatalf("create node status = %d, body = %s", create.Code, create.Body.String())
	}
	var node nodeResponse
	decodeResponse(t, create, &node)
	path := "/api/v1/nodes/" + node.ID + "/agent-updates"
	latestPath := path + "/latest"

	unauthenticated := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.2.0"}`), map[string]string{"Content-Type": "application/json"})
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated update status = %d", unauthenticated.Code)
	}
	withoutCSRF := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.2.0"}`), map[string]string{"Content-Type": "application/json"}, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("update without CSRF status = %d", withoutCSRF.Code)
	}
	beforeRegistration := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.2.0"}`), csrfHeaders, sessionCookie)
	if beforeRegistration.Code != http.StatusBadRequest {
		t.Fatalf("update before registration status = %d, body = %s", beforeRegistration.Code, beforeRegistration.Body.String())
	}
	missingLatest := performRequest(handler, http.MethodGet, latestPath, nil, nil, sessionCookie)
	if missingLatest.Code != http.StatusNotFound {
		t.Fatalf("missing latest update status = %d, body = %s", missingLatest.Code, missingLatest.Body.String())
	}

	credential := registerUpdatableAgent(t, handler, node.ID, sessionCookie, csrfCookie)
	invalid := performRequest(handler, http.MethodPost, path, []byte(`{"version":"v0.2.0"}`), csrfHeaders, sessionCookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid update version status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	requested := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.2.0"}`), csrfHeaders, sessionCookie)
	if requested.Code != http.StatusAccepted {
		t.Fatalf("request update status = %d, body = %s", requested.Code, requested.Body.String())
	}
	var update agentUpdateResponse
	decodeResponse(t, requested, &update)
	if update.NodeID != node.ID || update.Version != "0.2.0" || update.Status != "pending" || update.Attempts != 0 || update.ID == "" {
		t.Fatalf("requested update = %+v", update)
	}
	conflict := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.3.0"}`), csrfHeaders, sessionCookie)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("second pending update status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
	latest := performRequest(handler, http.MethodGet, latestPath, nil, nil, sessionCookie)
	if latest.Code != http.StatusOK {
		t.Fatalf("latest update status = %d, body = %s", latest.Code, latest.Body.String())
	}
	var latestUpdate agentUpdateResponse
	decodeResponse(t, latest, &latestUpdate)
	if latestUpdate.ID != update.ID || latestUpdate.Version != update.Version || latestUpdate.Status != "pending" {
		t.Fatalf("latest update = %+v", latestUpdate)
	}

	storedNode, err := database.NodeByID(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("NodeByID() error = %v", err)
	}
	if string(storedNode.CredentialHash) != string(credential) {
		t.Fatal("registered credential hash does not match returned node state")
	}
	command, err := database.AgentCommandByID(context.Background(), update.ID)
	if err != nil {
		t.Fatalf("AgentCommandByID() error = %v", err)
	}
	output, err := agentv1.EncodeAgentUpdateOutput(agentv1.AgentUpdateOutput{Version: "0.2.0", State: agentv1.AgentUpdateStateActivated})
	if err != nil {
		t.Fatalf("EncodeAgentUpdateOutput() error = %v", err)
	}
	if err := database.CompleteAgentCommand(context.Background(), node.ID, storedNode.CredentialHash, agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: time.Now().UTC(), Output: output,
	}, time.Now().UTC()); err != nil {
		t.Fatalf("CompleteAgentCommand() error = %v", err)
	}
	completed := performRequest(handler, http.MethodGet, latestPath, nil, nil, sessionCookie)
	if completed.Code != http.StatusOK {
		t.Fatalf("completed latest update status = %d, body = %s", completed.Code, completed.Body.String())
	}
	decodeResponse(t, completed, &latestUpdate)
	if latestUpdate.Status != "succeeded" || latestUpdate.CompletedAt == nil || latestUpdate.Problem != nil {
		t.Fatalf("completed update = %+v", latestUpdate)
	}

	failedRequest := performRequest(handler, http.MethodPost, path, []byte(`{"version":"0.3.0"}`), csrfHeaders, sessionCookie)
	if failedRequest.Code != http.StatusAccepted {
		t.Fatalf("request failed update status = %d, body = %s", failedRequest.Code, failedRequest.Body.String())
	}
	var failedUpdate agentUpdateResponse
	decodeResponse(t, failedRequest, &failedUpdate)
	failedCommand, err := database.AgentCommandByID(context.Background(), failedUpdate.ID)
	if err != nil {
		t.Fatalf("AgentCommandByID() failed update error = %v", err)
	}
	if err := database.CompleteAgentCommand(context.Background(), node.ID, storedNode.CredentialHash, agentv1.CommandResult{
		CommandID: failedCommand.ID, RequestSHA256: failedCommand.RequestSHA256, Status: agentv1.CommandStatusFailed,
		CompletedAt: time.Now().UTC(), Problem: &protocol.Problem{
			Code: protocol.ErrorUnavailable, Message: "release manifest unavailable", Retryable: true,
		},
	}, time.Now().UTC()); err != nil {
		t.Fatalf("CompleteAgentCommand() failed result error = %v", err)
	}
	failedLatest := performRequest(handler, http.MethodGet, latestPath, nil, nil, sessionCookie)
	if failedLatest.Code != http.StatusOK {
		t.Fatalf("failed latest update status = %d, body = %s", failedLatest.Code, failedLatest.Body.String())
	}
	decodeResponse(t, failedLatest, &failedUpdate)
	if failedUpdate.Status != "failed" || failedUpdate.Problem == nil ||
		failedUpdate.Problem.Code != protocol.ErrorUnavailable || failedUpdate.Problem.Message != "release manifest unavailable" {
		t.Fatalf("failed update = %+v", failedUpdate)
	}

	unauthenticatedLatest := performRequest(handler, http.MethodGet, latestPath, nil, nil)
	if unauthenticatedLatest.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated latest update status = %d", unauthenticatedLatest.Code)
	}
	unknownLatest := performRequest(handler, http.MethodGet, "/api/v1/nodes/"+uuid.NewString()+"/agent-updates/latest", nil, nil, sessionCookie)
	if unknownLatest.Code != http.StatusNotFound {
		t.Fatalf("unknown node latest update status = %d, body = %s", unknownLatest.Code, unknownLatest.Body.String())
	}
}

func registerUpdatableAgent(t *testing.T, handler http.Handler, nodeID string, sessionCookie, csrfCookie *http.Cookie) []byte {
	t.Helper()
	tokenResponse := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+nodeID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if tokenResponse.Code != http.StatusCreated {
		t.Fatalf("create registration token status = %d, body = %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var token struct {
		Value string `json:"token"`
	}
	decodeResponse(t, tokenResponse, &token)
	body, err := json.Marshal(agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: token.Value, AgentVersion: "0.1.0",
		Hostname: "edge", OS: "linux", Arch: "amd64", Capabilities: []string{
			agentv1.CapabilityAgentSelfUpdate,
			agentv1.CapabilityControlCommands,
		},
	})
	if err != nil {
		t.Fatalf("marshal registration request: %v", err)
	}
	response := performRequest(handler, http.MethodPost, "/api/v1/agent/register", body, map[string]string{"Content-Type": "application/json"})
	if response.Code != http.StatusCreated {
		t.Fatalf("register Agent status = %d, body = %s", response.Code, response.Body.String())
	}
	var registered agentv1.RegisterResponse
	decodeResponse(t, response, &registered)
	if registered.NodeID != nodeID {
		t.Fatalf("registered Agent = %+v", registered)
	}
	return credentialHashForTest(t, registered.Credential)
}

func credentialHashForTest(t *testing.T, credential string) []byte {
	t.Helper()
	if err := agentv1.ValidateCredential(credential); err != nil {
		t.Fatalf("registered credential = %q: %v", credential, err)
	}
	return auth.TokenHash(credential)
}
