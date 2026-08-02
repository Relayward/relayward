package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type resetRuleRequest struct {
	Kind         string  `json:"kind"`
	Value        *int    `json:"value"`
	Timezone     string  `json:"timezone"`
	PeriodAnchor *string `json:"period_anchor"`
}

type authorizationRequest struct {
	UserID                string           `json:"user_id,omitempty"`
	NodeID                string           `json:"node_id,omitempty"`
	Enabled               bool             `json:"enabled"`
	TrafficLimitBytes     *int64           `json:"traffic_limit_bytes"`
	Reset                 resetRuleRequest `json:"reset"`
	ExpiresAt             *string          `json:"expires_at"`
	SoftIPLimit           *int             `json:"soft_ip_limit"`
	ActivityWindowSeconds int              `json:"activity_window_seconds"`
	BlockDurationSeconds  int              `json:"block_duration_seconds"`
}

type resetRuleResponse struct {
	Kind         string     `json:"kind"`
	Value        *int       `json:"value"`
	Timezone     string     `json:"timezone"`
	PeriodAnchor *time.Time `json:"period_anchor"`
}

type authorizationResponse struct {
	ID                    string            `json:"id"`
	UserID                string            `json:"user_id"`
	NodeID                string            `json:"node_id"`
	Enabled               bool              `json:"enabled"`
	TrafficLimitBytes     *int64            `json:"traffic_limit_bytes"`
	Reset                 resetRuleResponse `json:"reset"`
	ExpiresAt             *time.Time        `json:"expires_at"`
	SoftIPLimit           *int              `json:"soft_ip_limit"`
	ActivityWindowSeconds int               `json:"activity_window_seconds"`
	BlockDurationSeconds  int               `json:"block_duration_seconds"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

func (server *Server) listAuthorizations(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListAuthorizations(request.Context(), request.URL.Query().Get("user_id"), request.URL.Query().Get("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	items := make([]authorizationResponse, len(values))
	for index, value := range values {
		items[index] = authorizationView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) getAuthorization(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.Authorization(request.Context(), request.PathValue("authorization_id"))
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	writeJSON(w, http.StatusOK, authorizationView(value))
}

func (server *Server) createAuthorization(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, err := decodeAuthorizationRequest(request)
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	created, err := server.management.CreateAuthorization(request.Context(), input)
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"authorization": authorizationView(created.Authorization), "subscription_token": created.SubscriptionToken,
	})
}

func (server *Server) updateAuthorization(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, err := decodeAuthorizationRequest(request)
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	value, err := server.management.UpdateAuthorization(request.Context(), request.PathValue("authorization_id"), input)
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	writeJSON(w, http.StatusOK, authorizationView(value))
}

func (server *Server) deleteAuthorization(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteAuthorization(request.Context(), request.PathValue("authorization_id")); err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) rotateSubscriptionToken(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	token, err := server.management.RotateSubscriptionToken(request.Context(), request.PathValue("authorization_id"))
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscription_token": token.Token, "rotated_at": token.RotatedAt})
}

func decodeAuthorizationRequest(request *http.Request) (management.AuthorizationInput, error) {
	var input authorizationRequest
	if err := decodeJSON(request, &input); err != nil {
		return management.AuthorizationInput{}, &management.FieldError{Field: "body", Description: "must be a valid authorization request"}
	}
	periodAnchor, err := parseOptionalTime("reset.period_anchor", input.Reset.PeriodAnchor)
	if err != nil {
		return management.AuthorizationInput{}, err
	}
	expiresAt, err := parseOptionalTime("expires_at", input.ExpiresAt)
	if err != nil {
		return management.AuthorizationInput{}, err
	}
	return management.AuthorizationInput{
		UserID: input.UserID, NodeID: input.NodeID, Enabled: input.Enabled,
		TrafficLimitBytes: input.TrafficLimitBytes,
		Reset:             management.ResetRule{Kind: input.Reset.Kind, Value: input.Reset.Value, Timezone: input.Reset.Timezone, PeriodAnchor: periodAnchor},
		ExpiresAt:         expiresAt, SoftIPLimit: input.SoftIPLimit, ActivityWindowSeconds: input.ActivityWindowSeconds,
		BlockDurationSeconds: input.BlockDurationSeconds,
	}, nil
}

func parseOptionalTime(field string, value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, &management.FieldError{Field: field, Description: "must be an RFC 3339 timestamp or null"}
	}
	parsed = parsed.UTC().Truncate(time.Second)
	return &parsed, nil
}

func authorizationView(value store.Authorization) authorizationResponse {
	return authorizationResponse{
		ID: value.ID, UserID: value.UserID, NodeID: value.NodeID, Enabled: value.Enabled,
		TrafficLimitBytes: value.TrafficLimitBytes,
		Reset:             resetRuleResponse{Kind: value.ResetKind, Value: value.ResetValue, Timezone: value.Timezone, PeriodAnchor: value.PeriodAnchor},
		ExpiresAt:         value.ExpiresAt, SoftIPLimit: value.SoftIPLimit,
		ActivityWindowSeconds: value.ActivityWindowSeconds, BlockDurationSeconds: value.BlockDurationSeconds,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type serviceBindingRequest struct {
	PluginID  string `json:"plugin_id,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	Enabled   *bool  `json:"enabled"`
}

type serviceBindingResponse struct {
	ID              string    `json:"id"`
	AuthorizationID string    `json:"authorization_id"`
	PluginID        string    `json:"plugin_id"`
	ServiceID       string    `json:"service_id"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (server *Server) listServiceBindings(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListServiceBindings(request.Context(), request.PathValue("authorization_id"))
	if err != nil {
		server.resourceError(w, request, err, "Authorization")
		return
	}
	items := make([]serviceBindingResponse, len(values))
	for index, value := range values {
		items[index] = serviceBindingView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) createServiceBinding(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input serviceBindingRequest
	if err := decodeJSON(request, &input); err != nil || input.Enabled == nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid service binding request.", false)
		return
	}
	value, err := server.management.CreateServiceBinding(request.Context(), management.ServiceBindingInput{
		AuthorizationID: request.PathValue("authorization_id"), PluginID: input.PluginID, ServiceID: input.ServiceID, Enabled: *input.Enabled,
	})
	if err != nil {
		server.resourceError(w, request, err, "Service binding")
		return
	}
	writeJSON(w, http.StatusCreated, serviceBindingView(value))
}

func (server *Server) updateServiceBinding(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input serviceBindingRequest
	if err := decodeJSON(request, &input); err != nil || input.Enabled == nil || input.PluginID != "" || input.ServiceID != "" {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid service binding request.", false)
		return
	}
	value, err := server.management.UpdateServiceBinding(request.Context(), request.PathValue("binding_id"), *input.Enabled)
	if err != nil {
		server.resourceError(w, request, err, "Service binding")
		return
	}
	writeJSON(w, http.StatusOK, serviceBindingView(value))
}

func (server *Server) deleteServiceBinding(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteServiceBinding(request.Context(), request.PathValue("binding_id")); err != nil {
		server.resourceError(w, request, err, "Service binding")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func serviceBindingView(value store.ServiceBinding) serviceBindingResponse {
	return serviceBindingResponse{
		ID: value.ID, AuthorizationID: value.AuthorizationID, PluginID: value.PluginID, ServiceID: value.ServiceID,
		Enabled: value.Enabled, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type auditResponse struct {
	ID         int64          `json:"id"`
	OccurredAt time.Time      `json:"occurred_at"`
	ActorType  string         `json:"actor_type"`
	ActorID    string         `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Outcome    string         `json:"outcome"`
	Metadata   map[string]any `json:"metadata"`
}

func (server *Server) listAudit(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	beforeID, err := parseQueryInt(request, "before_id")
	if err != nil {
		server.resourceError(w, request, err, "Audit entry")
		return
	}
	limit, err := parseQueryInt(request, "limit")
	if err != nil {
		server.resourceError(w, request, err, "Audit entry")
		return
	}
	values, err := server.management.ListAudit(request.Context(), beforeID, int(limit))
	if err != nil {
		server.resourceError(w, request, err, "Audit entry")
		return
	}
	items := make([]auditResponse, len(values))
	for index, value := range values {
		items[index] = auditResponse{
			ID: value.ID, OccurredAt: value.OccurredAt, ActorType: value.ActorType, ActorID: value.ActorID,
			Action: value.Action, TargetType: value.TargetType, TargetID: value.TargetID, Outcome: value.Outcome, Metadata: value.Metadata,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func parseQueryInt(request *http.Request, name string) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, &management.FieldError{Field: name, Description: "must be an integer"}
	}
	return parsed, nil
}
