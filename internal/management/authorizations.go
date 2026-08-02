package management

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/store"
)

const (
	defaultActivityWindow = 10 * time.Minute
	defaultBlockDuration  = 30 * time.Minute
)

var componentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type ResetRule struct {
	Kind         string
	Value        *int
	Timezone     string
	PeriodAnchor *time.Time
}

type AuthorizationInput struct {
	UserID                string
	NodeID                string
	Enabled               bool
	TrafficLimitBytes     *int64
	Reset                 ResetRule
	ExpiresAt             *time.Time
	SoftIPLimit           *int
	ActivityWindowSeconds int
	BlockDurationSeconds  int
}

type CreatedAuthorization struct {
	Authorization     store.Authorization
	SubscriptionToken string
}

type RotatedSubscriptionToken struct {
	Token     string
	RotatedAt time.Time
}

func DefaultAuthorizationInput(userID, nodeID string) AuthorizationInput {
	return AuthorizationInput{
		UserID: userID, NodeID: nodeID, Enabled: true,
		Reset:                 ResetRule{Kind: "never", Timezone: "UTC"},
		ActivityWindowSeconds: int(defaultActivityWindow.Seconds()),
		BlockDurationSeconds:  int(defaultBlockDuration.Seconds()),
	}
}

func (service *Service) ListAuthorizations(ctx context.Context, userID, nodeID string) ([]store.Authorization, error) {
	if userID != "" {
		if err := validateID("user_id", userID); err != nil {
			return nil, err
		}
	}
	if nodeID != "" {
		if err := validateID("node_id", nodeID); err != nil {
			return nil, err
		}
	}
	return service.store.ListAuthorizations(ctx, userID, nodeID)
}

func (service *Service) Authorization(ctx context.Context, id string) (store.Authorization, error) {
	if err := validateID("authorization_id", id); err != nil {
		return store.Authorization{}, err
	}
	return service.store.AuthorizationByID(ctx, id)
}

func (service *Service) CreateAuthorization(ctx context.Context, input AuthorizationInput) (CreatedAuthorization, error) {
	value, err := normalizeAuthorization(uuid.NewString(), input)
	if err != nil {
		return CreatedAuthorization{}, err
	}
	token, hash, err := newSubscriptionToken()
	if err != nil {
		return CreatedAuthorization{}, err
	}
	value.SubscriptionTokenHash = hash
	now := service.currentTime()
	value.CreatedAt = now
	value.UpdatedAt = now
	if err := service.store.CreateAuthorization(ctx, value, now); err != nil {
		return CreatedAuthorization{}, err
	}
	return CreatedAuthorization{Authorization: value, SubscriptionToken: token}, nil
}

func (service *Service) UpdateAuthorization(ctx context.Context, id string, input AuthorizationInput) (store.Authorization, error) {
	if err := validateID("authorization_id", id); err != nil {
		return store.Authorization{}, err
	}
	current, err := service.store.AuthorizationByID(ctx, id)
	if err != nil {
		return store.Authorization{}, err
	}
	input.UserID = current.UserID
	input.NodeID = current.NodeID
	value, err := normalizeAuthorization(id, input)
	if err != nil {
		return store.Authorization{}, err
	}
	if err := service.store.UpdateAuthorization(ctx, value, service.currentTime()); err != nil {
		return store.Authorization{}, err
	}
	return service.store.AuthorizationByID(ctx, id)
}

func (service *Service) DeleteAuthorization(ctx context.Context, id string) error {
	if err := validateID("authorization_id", id); err != nil {
		return err
	}
	return service.store.DeleteAuthorization(ctx, id, service.currentTime())
}

func (service *Service) RotateSubscriptionToken(ctx context.Context, id string) (RotatedSubscriptionToken, error) {
	if err := validateID("authorization_id", id); err != nil {
		return RotatedSubscriptionToken{}, err
	}
	token, hash, err := newSubscriptionToken()
	if err != nil {
		return RotatedSubscriptionToken{}, err
	}
	now := service.currentTime()
	if err := service.store.RotateSubscriptionToken(ctx, id, hash, now); err != nil {
		return RotatedSubscriptionToken{}, err
	}
	return RotatedSubscriptionToken{Token: token, RotatedAt: now}, nil
}

type ServiceBindingInput struct {
	AuthorizationID string
	PluginID        string
	ServiceID       string
	Enabled         bool
}

func (service *Service) ListServiceBindings(ctx context.Context, authorizationID string) ([]store.ServiceBinding, error) {
	if err := validateID("authorization_id", authorizationID); err != nil {
		return nil, err
	}
	if _, err := service.store.AuthorizationByID(ctx, authorizationID); err != nil {
		return nil, err
	}
	return service.store.ListServiceBindings(ctx, authorizationID)
}

func (service *Service) CreateServiceBinding(ctx context.Context, input ServiceBindingInput) (store.ServiceBinding, error) {
	if err := validateID("authorization_id", input.AuthorizationID); err != nil {
		return store.ServiceBinding{}, err
	}
	input.PluginID = strings.TrimSpace(input.PluginID)
	if !componentIDPattern.MatchString(input.PluginID) {
		return store.ServiceBinding{}, invalid("plugin_id", "must be a lowercase component ID")
	}
	input.ServiceID = strings.TrimSpace(input.ServiceID)
	if !componentIDPattern.MatchString(input.ServiceID) {
		return store.ServiceBinding{}, invalid("service_id", "must be a lowercase component ID")
	}
	now := service.currentTime()
	value := store.ServiceBinding{
		ID: uuid.NewString(), AuthorizationID: input.AuthorizationID, PluginID: input.PluginID,
		ServiceID: input.ServiceID, Enabled: input.Enabled, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.store.CreateServiceBinding(ctx, value, now); err != nil {
		return store.ServiceBinding{}, err
	}
	return value, nil
}

func (service *Service) UpdateServiceBinding(ctx context.Context, id string, enabled bool) (store.ServiceBinding, error) {
	if err := validateID("binding_id", id); err != nil {
		return store.ServiceBinding{}, err
	}
	if err := service.store.UpdateServiceBinding(ctx, id, enabled, service.currentTime()); err != nil {
		return store.ServiceBinding{}, err
	}
	return service.store.ServiceBindingByID(ctx, id)
}

func (service *Service) DeleteServiceBinding(ctx context.Context, id string) error {
	if err := validateID("binding_id", id); err != nil {
		return err
	}
	return service.store.DeleteServiceBinding(ctx, id, service.currentTime())
}

func (service *Service) ListAudit(ctx context.Context, beforeID int64, limit int) ([]store.AuditEntry, error) {
	if beforeID < 0 {
		return nil, invalid("before_id", "must not be negative")
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return nil, invalid("limit", "must be between 1 and 200")
	}
	return service.store.ListAudit(ctx, beforeID, limit)
}

func normalizeAuthorization(id string, input AuthorizationInput) (store.Authorization, error) {
	if err := validateID("user_id", input.UserID); err != nil {
		return store.Authorization{}, err
	}
	if err := validateID("node_id", input.NodeID); err != nil {
		return store.Authorization{}, err
	}
	if input.TrafficLimitBytes != nil && *input.TrafficLimitBytes < 0 {
		return store.Authorization{}, invalid("traffic_limit_bytes", "must not be negative")
	}
	rule, err := normalizeResetRule(input.Reset)
	if err != nil {
		return store.Authorization{}, err
	}
	if input.SoftIPLimit != nil && (*input.SoftIPLimit < 1 || *input.SoftIPLimit > 1024) {
		return store.Authorization{}, invalid("soft_ip_limit", "must be between 1 and 1024")
	}
	if input.ActivityWindowSeconds < 60 || input.ActivityWindowSeconds > 86400 {
		return store.Authorization{}, invalid("activity_window_seconds", "must be between 60 and 86400")
	}
	if input.BlockDurationSeconds < 60 || input.BlockDurationSeconds > 604800 {
		return store.Authorization{}, invalid("block_duration_seconds", "must be between 60 and 604800")
	}
	return store.Authorization{
		ID: id, UserID: input.UserID, NodeID: input.NodeID, Enabled: input.Enabled,
		TrafficLimitBytes: input.TrafficLimitBytes, ResetKind: rule.Kind, ResetValue: rule.Value,
		Timezone: rule.Timezone, PeriodAnchor: truncateOptionalTime(rule.PeriodAnchor),
		ExpiresAt: truncateOptionalTime(input.ExpiresAt), SoftIPLimit: input.SoftIPLimit,
		ActivityWindowSeconds: input.ActivityWindowSeconds, BlockDurationSeconds: input.BlockDurationSeconds,
	}, nil
}

func normalizeResetRule(rule ResetRule) (ResetRule, error) {
	rule.Kind = strings.TrimSpace(rule.Kind)
	rule.Timezone = strings.TrimSpace(rule.Timezone)
	if rule.Timezone == "" {
		return ResetRule{}, invalid("reset.timezone", "is required")
	}
	if rule.Timezone == "Local" {
		return ResetRule{}, invalid("reset.timezone", "must be UTC or an IANA timezone")
	}
	if _, err := time.LoadLocation(rule.Timezone); err != nil {
		return ResetRule{}, invalid("reset.timezone", "must be UTC or an IANA timezone")
	}
	switch rule.Kind {
	case "never", "daily":
		if rule.Value != nil || rule.PeriodAnchor != nil {
			return ResetRule{}, invalid("reset", "does not accept value or period_anchor for this kind")
		}
	case "weekly":
		if rule.Value == nil || *rule.Value < 1 || *rule.Value > 7 || rule.PeriodAnchor != nil {
			return ResetRule{}, invalid("reset.value", "must be an ISO weekday from 1 to 7")
		}
	case "monthly":
		if rule.Value == nil || *rule.Value < 1 || *rule.Value > 31 || rule.PeriodAnchor != nil {
			return ResetRule{}, invalid("reset.value", "must be a day from 1 to 31")
		}
	case "interval_days":
		if rule.Value == nil || *rule.Value < 1 || *rule.Value > 3650 {
			return ResetRule{}, invalid("reset.value", "must be between 1 and 3650")
		}
		if rule.PeriodAnchor == nil {
			return ResetRule{}, invalid("reset.period_anchor", "is required")
		}
	default:
		return ResetRule{}, invalid("reset.kind", "must be never, daily, weekly, monthly, or interval_days")
	}
	return rule, nil
}

func newSubscriptionToken() (string, []byte, error) {
	value, err := auth.NewToken(32)
	if err != nil {
		return "", nil, fmt.Errorf("generate subscription token: %w", err)
	}
	token := "rws_" + value
	return token, auth.TokenHash(token), nil
}

func truncateOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC().Truncate(time.Second)
	return &result
}
