package server

import (
	"net/http"
	"time"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type systemSettingsRequest struct {
	SessionLifetimeMinutes   int    `json:"session_lifetime_minutes"`
	Timezone                 string `json:"timezone"`
	PublicURL                string `json:"public_url"`
	SubscriptionTitle        string `json:"subscription_title"`
	SupportURL               string `json:"support_url"`
	ProfileURL               string `json:"profile_url"`
	SubscriptionRefreshHours int    `json:"subscription_refresh_hours"`
}

type systemSettingsResponse struct {
	SessionLifetimeMinutes   int       `json:"session_lifetime_minutes"`
	Timezone                 string    `json:"timezone"`
	PublicURL                string    `json:"public_url"`
	SubscriptionTitle        string    `json:"subscription_title"`
	SupportURL               string    `json:"support_url"`
	ProfileURL               string    `json:"profile_url"`
	SubscriptionRefreshHours int       `json:"subscription_refresh_hours"`
	UpdatedAt                time.Time `json:"updated_at"`
}

func (server *Server) getSystemSettings(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.SystemSettings(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, systemSettingsView(value))
}

func (server *Server) updateSystemSettings(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input systemSettingsRequest
	if err := decodeJSON(request, &input); err != nil {
		server.resourceError(w, request, &management.FieldError{Field: "settings", Description: "invalid JSON request"}, "System settings")
		return
	}
	value, err := server.management.UpdateSystemSettings(request.Context(), management.SystemSettingsInput{
		SessionLifetimeMinutes: input.SessionLifetimeMinutes, Timezone: input.Timezone, PublicURL: input.PublicURL,
		SubscriptionTitle: input.SubscriptionTitle, SupportURL: input.SupportURL, ProfileURL: input.ProfileURL,
		SubscriptionRefreshHours: input.SubscriptionRefreshHours,
	})
	if err != nil {
		server.resourceError(w, request, err, "System settings")
		return
	}
	writeJSON(w, http.StatusOK, systemSettingsView(value))
}

func systemSettingsView(value store.SystemSettings) systemSettingsResponse {
	return systemSettingsResponse{
		SessionLifetimeMinutes: value.SessionLifetimeMinutes, Timezone: value.Timezone, PublicURL: value.PublicURL,
		SubscriptionTitle: value.SubscriptionTitle, SupportURL: value.SupportURL, ProfileURL: value.ProfileURL,
		SubscriptionRefreshHours: value.SubscriptionRefreshHours, UpdatedAt: value.UpdatedAt,
	}
}
