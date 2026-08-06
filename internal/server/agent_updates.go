package server

import (
	"errors"
	"net/http"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/agentrelease"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type agentUpdateRequest struct {
	Version string `json:"version"`
}

type agentUpdateResponse struct {
	ID          string            `json:"id"`
	NodeID      string            `json:"node_id"`
	Version     string            `json:"version"`
	Status      string            `json:"status"`
	Attempts    int               `json:"attempts"`
	LastSentAt  *time.Time        `json:"last_sent_at"`
	Problem     *protocol.Problem `json:"problem,omitempty"`
	CompletedAt *time.Time        `json:"completed_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type agentReleaseResponse struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"published_at"`
	CheckedAt   time.Time `json:"checked_at"`
}

type agentUpdateAvailabilityResponse struct {
	CurrentVersion string               `json:"current_version"`
	LatestRelease  agentReleaseResponse `json:"latest_release"`
	Relation       string               `json:"relation"`
}

func (server *Server) requestAgentUpdate(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input agentUpdateRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid Agent update request.", false)
		return
	}
	value, err := server.management.RequestAgentUpdate(request.Context(), request.PathValue("node_id"), input.Version)
	if err != nil {
		server.resourceError(w, request, err, "Agent update")
		return
	}
	response, err := agentUpdateView(value)
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (server *Server) latestAgentUpdate(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.LatestAgentUpdate(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Agent update")
		return
	}
	response, err := agentUpdateView(value)
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (server *Server) agentUpdateAvailability(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.AgentUpdateAvailability(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.agentReleaseError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, agentUpdateAvailabilityResponse{
		CurrentVersion: value.CurrentVersion,
		LatestRelease:  agentReleaseView(value.LatestRelease),
		Relation:       value.Relation,
	})
}

func (server *Server) requestLatestAgentUpdate(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.RequestLatestAgentUpdate(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.agentReleaseError(w, request, err)
		return
	}
	response, err := agentUpdateView(value)
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (server *Server) agentReleaseError(w http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, management.ErrUpstreamUnavailable) {
		server.logger.Warn("Agent release lookup failed", "error", err)
	}
	server.resourceError(w, request, err, "Agent release")
}

func agentReleaseView(value agentrelease.Release) agentReleaseResponse {
	return agentReleaseResponse{
		Version: value.Version, Tag: value.Tag, PublishedAt: value.PublishedAt, CheckedAt: value.CheckedAt,
	}
}

func agentUpdateView(value store.AgentCommand) (agentUpdateResponse, error) {
	update, err := agentv1.DecodeAgentUpdateCommand(value.Request)
	if err != nil {
		return agentUpdateResponse{}, err
	}
	var problem *protocol.Problem
	if value.Result != nil {
		problem = value.Result.Problem
	}
	return agentUpdateResponse{
		ID: value.ID, NodeID: value.NodeID, Version: update.Version, Status: value.Status,
		Attempts: value.Attempts, LastSentAt: value.LastSentAt, Problem: problem,
		CompletedAt: value.CompletedAt, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}, nil
}
