package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

type nodeRequest struct {
	Name          string `json:"name"`
	PublicAddress string `json:"public_address"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

type nodeResponse struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	PublicAddress  string              `json:"public_address"`
	Enabled        bool                `json:"enabled"`
	AgentStatus    string              `json:"agent_status"`
	Hostname       string              `json:"hostname"`
	AgentVersion   string              `json:"agent_version"`
	AgentOS        string              `json:"agent_os"`
	AgentArch      string              `json:"agent_arch"`
	Capabilities   []string            `json:"capabilities"`
	AgentStartedAt *time.Time          `json:"agent_started_at"`
	Policy         *nodePolicyResponse `json:"policy"`
	RegisteredAt   *time.Time          `json:"registered_at"`
	LastSeenAt     *time.Time          `json:"last_seen_at"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func (server *Server) listNodes(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListNodes(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	policyStates, err := server.store.ListNodePolicyStates(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	policies := make(map[string]store.NodePolicyState, len(policyStates))
	for _, state := range policyStates {
		policies[state.NodeID] = state
	}
	items := make([]nodeResponse, len(values))
	for index, value := range values {
		state, exists := policies[value.ID]
		items[index] = server.nodeViewWithPolicy(value, state, exists)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) getNode(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.Node(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	state, stateErr := server.store.NodePolicyStateByID(request.Context(), value.ID)
	if stateErr != nil && !errors.Is(stateErr, store.ErrNotFound) {
		server.internalError(w, request, stateErr)
		return
	}
	writeJSON(w, http.StatusOK, server.nodeViewWithPolicy(value, state, stateErr == nil))
}

func (server *Server) createNode(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input nodeRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node request.", false)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	value, err := server.management.CreateNode(request.Context(), management.NodeInput{Name: input.Name, PublicAddress: input.PublicAddress, Enabled: enabled})
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	writeJSON(w, http.StatusCreated, server.nodeView(value))
}

func (server *Server) updateNode(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input nodeRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node request.", false)
		return
	}
	if input.Enabled == nil {
		writeProblemWithViolations(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node request.", false,
			[]protocol.FieldViolation{{Field: "enabled", Description: "is required"}})
		return
	}
	value, err := server.management.UpdateNode(request.Context(), request.PathValue("node_id"), management.NodeInput{
		Name: input.Name, PublicAddress: input.PublicAddress, Enabled: *input.Enabled,
	})
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	if server.policyCoordinator != nil {
		if _, err := server.policyCoordinator.ReconcileNode(request.Context(), value.ID); err != nil {
			server.internalError(w, request, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, server.nodeView(value))
}

func (server *Server) deleteNode(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	nodeID := request.PathValue("node_id")
	if err := server.management.DeleteNode(request.Context(), nodeID); err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	server.agentSessions.disconnect(nodeID)
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) revokeNodeCredential(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	nodeID := request.PathValue("node_id")
	value, err := server.management.RevokeNodeCredential(request.Context(), nodeID)
	if err != nil {
		server.resourceError(w, request, err, "Node credential")
		return
	}
	server.agentSessions.disconnect(nodeID)
	writeJSON(w, http.StatusOK, server.nodeView(value))
}

func (server *Server) createNodeRegistrationToken(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.CreateRegistrationToken(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": value.Token, "expires_at": value.ExpiresAt})
}

func (server *Server) nodeView(value store.Node) nodeResponse {
	return server.nodeViewWithPolicy(value, store.NodePolicyState{}, false)
}

type nodePolicyResponse struct {
	DesiredGeneration uint64            `json:"desired_generation"`
	AppliedGeneration uint64            `json:"applied_generation"`
	Status            string            `json:"status"`
	LastProblem       *protocol.Problem `json:"last_problem,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

func (server *Server) nodeViewWithPolicy(value store.Node, policy store.NodePolicyState, hasPolicy bool) nodeResponse {
	status := "pending"
	if !value.Enabled {
		status = "disabled"
	} else if server.agentSessions.connected(value.ID) {
		status = "online"
	} else if value.RegisteredAt != nil {
		status = "offline"
	}
	capabilities := value.Capabilities
	if capabilities == nil {
		capabilities = []string{}
	}
	var policyView *nodePolicyResponse
	if hasPolicy {
		policyView = &nodePolicyResponse{
			DesiredGeneration: policy.DesiredGeneration, AppliedGeneration: policy.AppliedGeneration,
			Status: policy.ReconcileStatus, LastProblem: policy.LastProblem, UpdatedAt: policy.UpdatedAt,
		}
	}
	return nodeResponse{
		ID: value.ID, Name: value.Name, PublicAddress: value.PublicAddress, Enabled: value.Enabled,
		AgentStatus: status, Hostname: value.Hostname, AgentVersion: value.AgentVersion,
		AgentOS: value.AgentOS, AgentArch: value.AgentArch, Capabilities: capabilities,
		AgentStartedAt: value.AgentStartedAt, Policy: policyView,
		RegisteredAt: value.RegisteredAt, LastSeenAt: value.LastSeenAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type userRequest struct {
	DisplayName string  `json:"display_name"`
	Email       *string `json:"email"`
	Telegram    *string `json:"telegram"`
	Note        string  `json:"note"`
}

type userResponse struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       *string   `json:"email"`
	Telegram    *string   `json:"telegram"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (server *Server) listUsers(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListUsers(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]userResponse, len(values))
	for index, value := range values {
		items[index] = userView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) getUser(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.User(request.Context(), request.PathValue("user_id"))
	if err != nil {
		server.resourceError(w, request, err, "User")
		return
	}
	writeJSON(w, http.StatusOK, userView(value))
}

func (server *Server) createUser(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input userRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid user request.", false)
		return
	}
	value, err := server.management.CreateUser(request.Context(), management.UserInput{
		DisplayName: input.DisplayName, Email: input.Email, Telegram: input.Telegram, Note: input.Note,
	})
	if err != nil {
		server.resourceError(w, request, err, "User")
		return
	}
	writeJSON(w, http.StatusCreated, userView(value))
}

func (server *Server) updateUser(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input userRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid user request.", false)
		return
	}
	value, err := server.management.UpdateUser(request.Context(), request.PathValue("user_id"), management.UserInput{
		DisplayName: input.DisplayName, Email: input.Email, Telegram: input.Telegram, Note: input.Note,
	})
	if err != nil {
		server.resourceError(w, request, err, "User")
		return
	}
	writeJSON(w, http.StatusOK, userView(value))
}

func (server *Server) deleteUser(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteUser(request.Context(), request.PathValue("user_id")); err != nil {
		server.resourceError(w, request, err, "User")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func userView(value store.User) userResponse {
	return userResponse{
		ID: value.ID, DisplayName: value.DisplayName, Email: value.Email, Telegram: value.Telegram, Note: value.Note,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func (server *Server) resourceError(w http.ResponseWriter, request *http.Request, err error, resource string) {
	var fieldError *management.FieldError
	switch {
	case errors.As(err, &fieldError):
		writeProblemWithViolations(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid request.", false,
			[]protocol.FieldViolation{{Field: fieldError.Field, Description: fieldError.Description}})
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusNotFound, protocol.ErrorNotFound, resource+" not found.", false)
	case errors.Is(err, store.ErrConflict):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, resource+" conflicts with existing data.", false)
	case errors.Is(err, store.ErrStateConflict):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, resource+" state changed before the operation completed.", true)
	case errors.Is(err, secretbox.ErrUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Encrypted secrets are unavailable.", false)
	case errors.Is(err, management.ErrUpstreamUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, resource+" is temporarily unavailable.", true)
	default:
		server.internalError(w, request, err)
	}
}
