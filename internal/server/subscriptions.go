package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type subscriptionServiceResponse struct {
	PluginID     string   `json:"plugin_id"`
	ServiceID    string   `json:"service_id"`
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities"`
}

type subscriptionResponse struct {
	Title             string                        `json:"title"`
	SupportURL        string                        `json:"support_url"`
	ProfileURL        string                        `json:"profile_url"`
	RefreshHours      int                           `json:"refresh_hours"`
	Status            string                        `json:"status"`
	UserName          string                        `json:"user_name"`
	NodeName          string                        `json:"node_name"`
	NodeAddress       string                        `json:"node_address"`
	TrafficLimitBytes *int64                        `json:"traffic_limit_bytes"`
	TrafficUsedBytes  *uint64                       `json:"traffic_used_bytes"`
	Reset             resetRuleResponse             `json:"reset"`
	ExpiresAt         *time.Time                    `json:"expires_at"`
	Services          []subscriptionServiceResponse `json:"services"`
	Announcement      *string                       `json:"announcement"`
}

type pluginServiceResponse struct {
	NodeID       string   `json:"node_id"`
	PluginID     string   `json:"plugin_id"`
	ServiceID    string   `json:"service_id"`
	DisplayName  string   `json:"display_name"`
	Enabled      bool     `json:"enabled"`
	Capabilities []string `json:"capabilities"`
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
	services := make([]subscriptionServiceResponse, len(snapshot.Services))
	for index, value := range snapshot.Services {
		services[index] = subscriptionServiceResponse{
			PluginID: value.PluginID, ServiceID: value.ServiceID, DisplayName: value.DisplayName,
			Capabilities: append([]string(nil), value.Capabilities...),
		}
	}
	writeJSON(w, http.StatusOK, subscriptionResponse{
		Title: snapshot.Settings.SubscriptionTitle, SupportURL: snapshot.Settings.SupportURL,
		ProfileURL: snapshot.Settings.ProfileURL, RefreshHours: snapshot.Settings.SubscriptionRefreshHours,
		Status: management.SubscriptionStatus(snapshot, time.Now()), UserName: snapshot.UserName,
		NodeName: snapshot.NodeName, NodeAddress: snapshot.NodeAddress,
		TrafficLimitBytes: snapshot.Authorization.TrafficLimitBytes, TrafficUsedBytes: snapshot.TrafficUsedBytes,
		Reset: resetRuleResponse{
			Kind: snapshot.Authorization.ResetKind, Value: snapshot.Authorization.ResetValue,
			Timezone: snapshot.Authorization.Timezone, PeriodAnchor: snapshot.Authorization.PeriodAnchor,
		},
		ExpiresAt: snapshot.Authorization.ExpiresAt, Services: services, Announcement: snapshot.Announcement,
	})
}

func (server *Server) downloadSubscription(format, mediaType, filename string) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		value, err := server.management.RenderSubscription(request.Context(), request.PathValue("subscription_token"), format)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeProblem(w, http.StatusNotFound, protocol.ErrorNotFound, "Subscription not found.", false)
			return
		case errors.Is(err, management.ErrSubscriptionInactive):
			writeProblem(w, http.StatusForbidden, protocol.ErrorPermissionDenied, "Subscription is not active.", false)
			return
		case errors.Is(err, management.ErrUpstreamUnavailable):
			writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Subscription rendering is temporarily unavailable.", true)
			return
		case err != nil:
			server.internalError(w, request, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Content-Type", mediaType)
		w.Header().Set("Last-Modified", value.RenderedAt.UTC().Format(http.TimeFormat))
		if value.Settings.SubscriptionRefreshHours > 0 {
			w.Header().Set("Profile-Update-Interval", strconv.Itoa(value.Settings.SubscriptionRefreshHours))
		}
		if value.Settings.SupportURL != "" {
			w.Header().Set("Support-URL", value.Settings.SupportURL)
		}
		if value.Settings.ProfileURL != "" {
			w.Header().Set("Profile-Web-Page-URL", value.Settings.ProfileURL)
		}
		if value.Cached {
			w.Header().Set("X-Relayward-Cache", "hit")
		} else {
			w.Header().Set("X-Relayward-Cache", "miss")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(value.Content)
	}
}

func (server *Server) listPluginServices(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListPluginServices(request.Context(), request.URL.Query().Get("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	items := make([]pluginServiceResponse, len(values))
	for index, value := range values {
		items[index] = pluginServiceResponse{
			NodeID: value.NodeID, PluginID: value.PluginID, ServiceID: value.ServiceID,
			DisplayName: value.DisplayName, Enabled: value.Enabled,
			Capabilities: append([]string(nil), value.Capabilities...),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) getAnnouncement(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	content, err := server.management.Announcement(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content})
}

func (server *Server) updateAnnouncement(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid announcement request.", false)
		return
	}
	content, err := server.management.UpdateAnnouncement(request.Context(), input.Content)
	if err != nil {
		server.resourceError(w, request, err, "Announcement")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content})
}
