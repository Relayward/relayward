package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/management"
)

const defaultRecentAccessLimit = 100

type accessEventResponse struct {
	ID              int64     `json:"id"`
	NodeID          string    `json:"node_id"`
	PluginID        string    `json:"plugin_id"`
	ServiceID       string    `json:"service_id"`
	AuthorizationID string    `json:"authorization_id"`
	SourceIP        string    `json:"source_ip,omitempty"`
	Destination     string    `json:"destination,omitempty"`
	DestinationPort uint32    `json:"destination_port,omitempty"`
	Network         string    `json:"network,omitempty"`
	Protocol        string    `json:"protocol,omitempty"`
	Action          string    `json:"action"`
	ObservedAt      time.Time `json:"observed_at"`
	ReceivedAt      time.Time `json:"received_at"`
}

func (server *Server) listRecentAccessEvents(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if server.eventStore == nil {
		server.internalError(w, request, errors.New("event store is not configured"))
		return
	}
	beforeID, err := parseQueryInt(request, "before_id")
	if err != nil || beforeID < 0 {
		if err == nil {
			err = &management.FieldError{Field: "before_id", Description: "must not be negative"}
		}
		server.resourceError(w, request, err, "Access event")
		return
	}
	limit, err := parseQueryInt(request, "limit")
	if err != nil || limit < 0 || limit > 500 {
		if err == nil {
			err = &management.FieldError{Field: "limit", Description: "must be between 1 and 500"}
		}
		server.resourceError(w, request, err, "Access event")
		return
	}
	if limit == 0 {
		limit = defaultRecentAccessLimit
	}
	nodeID := request.URL.Query().Get("node_id")
	if nodeID != "" {
		if _, err := server.management.Node(request.Context(), nodeID); err != nil {
			server.resourceError(w, request, err, "Node")
			return
		}
	}
	values, err := server.eventStore.RecentAccessEvents(request.Context(), nodeID, beforeID, int(limit))
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]accessEventResponse, len(values))
	for index, value := range values {
		items[index] = accessEventView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func accessEventView(value eventstore.AccessRecord) accessEventResponse {
	return accessEventResponse{
		ID: value.ID, NodeID: value.NodeID, PluginID: value.PluginID, ServiceID: value.ServiceID,
		AuthorizationID: value.AuthorizationID, SourceIP: value.SourceIP, Destination: value.Destination,
		DestinationPort: value.DestinationPort, Network: value.Network, Protocol: value.Protocol,
		Action: value.Action, ObservedAt: value.ObservedAt, ReceivedAt: value.ReceivedAt,
	}
}
