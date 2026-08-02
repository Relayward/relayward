package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/store"
)

type subscriptionResponse struct {
	Status            string            `json:"status"`
	UserName          string            `json:"user_name"`
	NodeName          string            `json:"node_name"`
	NodeAddress       string            `json:"node_address"`
	TrafficLimitBytes *int64            `json:"traffic_limit_bytes"`
	TrafficUsedBytes  *int64            `json:"traffic_used_bytes"`
	Reset             resetRuleResponse `json:"reset"`
	ExpiresAt         *time.Time        `json:"expires_at"`
	Services          []any             `json:"services"`
	Announcement      *string           `json:"announcement"`
}

func (server *Server) subscription(w http.ResponseWriter, request *http.Request) {
	snapshot, err := server.management.Subscription(request.Context(), request.PathValue("subscription_token"))
	if errors.Is(err, store.ErrNotFound) {
		writeProblem(w, http.StatusNotFound, protocol.ErrorNotFound, "Subscription not found.", false)
		return
	}
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionResponse{
		Status: subscriptionStatus(snapshot, time.Now()), UserName: snapshot.UserName,
		NodeName: snapshot.NodeName, NodeAddress: snapshot.NodeAddress,
		TrafficLimitBytes: snapshot.Authorization.TrafficLimitBytes,
		Reset: resetRuleResponse{
			Kind: snapshot.Authorization.ResetKind, Value: snapshot.Authorization.ResetValue,
			Timezone: snapshot.Authorization.Timezone, PeriodAnchor: snapshot.Authorization.PeriodAnchor,
		},
		ExpiresAt: snapshot.Authorization.ExpiresAt, Services: make([]any, 0),
	})
}

func subscriptionStatus(snapshot store.SubscriptionSnapshot, now time.Time) string {
	switch {
	case !snapshot.NodeEnabled:
		return "node_disabled"
	case !snapshot.Authorization.Enabled:
		return "disabled"
	case snapshot.Authorization.ExpiresAt != nil && !snapshot.Authorization.ExpiresAt.After(now):
		return "expired"
	default:
		return "active"
	}
}
