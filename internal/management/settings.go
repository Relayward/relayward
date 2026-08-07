package management

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Relayward/relayward/internal/store"
)

type SystemSettingsInput struct {
	SessionLifetimeMinutes   int
	Timezone                 string
	PublicURL                string
	SubscriptionTitle        string
	SupportURL               string
	ProfileURL               string
	SubscriptionRefreshHours int
}

func (service *Service) SystemSettings(ctx context.Context) (store.SystemSettings, error) {
	return service.store.SystemSettings(ctx)
}

func (service *Service) UpdateSystemSettings(ctx context.Context, input SystemSettingsInput) (store.SystemSettings, error) {
	value, err := normalizeSystemSettings(input)
	if err != nil {
		return store.SystemSettings{}, err
	}
	now := service.currentTime()
	value.UpdatedAt = now
	if err := service.store.UpdateSystemSettings(ctx, value, now); err != nil {
		return store.SystemSettings{}, err
	}
	return value, nil
}

func normalizeSystemSettings(input SystemSettingsInput) (store.SystemSettings, error) {
	if input.SessionLifetimeMinutes < 60 || input.SessionLifetimeMinutes > 525600 {
		return store.SystemSettings{}, invalid("session_lifetime_minutes", "must be between 60 and 525600")
	}
	timezone := strings.TrimSpace(input.Timezone)
	if timezone == "" || len(timezone) > 64 {
		return store.SystemSettings{}, invalid("timezone", "must contain 1 to 64 characters")
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return store.SystemSettings{}, invalid("timezone", "must be a valid IANA time zone")
	}
	publicURL, err := normalizeWebURL("public_url", input.PublicURL, true)
	if err != nil {
		return store.SystemSettings{}, err
	}
	supportURL, err := normalizeWebURL("support_url", input.SupportURL, false)
	if err != nil {
		return store.SystemSettings{}, err
	}
	profileURL, err := normalizeWebURL("profile_url", input.ProfileURL, false)
	if err != nil {
		return store.SystemSettings{}, err
	}
	title := strings.TrimSpace(input.SubscriptionTitle)
	if title == "" || utf8.RuneCountInString(title) > 100 || strings.ContainsAny(title, "\r\n") {
		return store.SystemSettings{}, invalid("subscription_title", "must contain 1 to 100 characters on one line")
	}
	if input.SubscriptionRefreshHours < 0 || input.SubscriptionRefreshHours > 8760 {
		return store.SystemSettings{}, invalid("subscription_refresh_hours", "must be between 0 and 8760")
	}
	return store.SystemSettings{
		SessionLifetimeMinutes: input.SessionLifetimeMinutes, Timezone: timezone, PublicURL: publicURL,
		SubscriptionTitle: title, SupportURL: supportURL, ProfileURL: profileURL,
		SubscriptionRefreshHours: input.SubscriptionRefreshHours,
	}, nil
}

func normalizeWebURL(field, raw string, originOnly bool) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return "", invalid(field, "must be a valid HTTP or HTTPS URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return "", invalid(field, "must be an absolute HTTP or HTTPS URL without credentials")
	}
	if originOnly && parsed.Path != "" && parsed.Path != "/" {
		return "", invalid(field, "must not contain a path")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalid(field, "must not contain a query or fragment")
	}
	if originOnly {
		parsed.Path = ""
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
