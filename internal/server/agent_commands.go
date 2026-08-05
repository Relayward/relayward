package server

import (
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

const (
	defaultAgentCommandHistory = 50
	maximumAgentCommandHistory = 100
)

type agentCommandResponse struct {
	ID          string            `json:"id"`
	NodeID      string            `json:"node_id"`
	Kind        string            `json:"kind"`
	ScopeKey    string            `json:"scope_key"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	LastSentAt  *time.Time        `json:"last_sent_at"`
	Problem     *protocol.Problem `json:"problem,omitempty"`
	CompletedAt *time.Time        `json:"completed_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (server *Server) listAgentCommands(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	limit, err := parseQueryInt(request, "limit")
	if err != nil || limit < 0 || limit > maximumAgentCommandHistory {
		if err == nil {
			err = &management.FieldError{Field: "limit", Description: "must be between 1 and 100"}
		}
		server.resourceError(w, request, err, "Agent command")
		return
	}
	if limit == 0 {
		limit = defaultAgentCommandHistory
	}
	values, err := server.management.ListAgentCommands(request.Context(), request.PathValue("node_id"), int(limit))
	if err != nil {
		server.resourceError(w, request, err, "Agent command")
		return
	}
	items := make([]agentCommandResponse, len(values))
	for index, value := range values {
		items[index] = agentCommandView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func agentCommandView(value store.AgentCommand) agentCommandResponse {
	var problem *protocol.Problem
	if value.Result != nil {
		problem = value.Result.Problem
	}
	return agentCommandResponse{
		ID: value.ID, NodeID: value.NodeID, Kind: value.Kind, ScopeKey: value.ScopeKey,
		Status: value.Status, Attempts: value.Attempts, LastSentAt: value.LastSentAt,
		Problem: problem, CompletedAt: value.CompletedAt, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
