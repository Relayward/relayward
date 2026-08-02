package management

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/store"
)

func TestAuthorizationLifecycleTokensAndBindings(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	user, err := service.CreateUser(ctx, UserInput{DisplayName: "Alice"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	node, err := service.CreateNode(ctx, NodeInput{Name: "Edge", Enabled: true})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	input := DefaultAuthorizationInput(user.ID, node.ID)
	created, err := service.CreateAuthorization(ctx, input)
	if err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}
	if !strings.HasPrefix(created.SubscriptionToken, "rws_") || created.Authorization.ResetKind != "never" {
		t.Fatalf("created authorization = %+v", created)
	}
	if string(created.Authorization.SubscriptionTokenHash) == created.SubscriptionToken {
		t.Fatal("authorization retained the raw subscription token")
	}
	subscription, err := service.Subscription(ctx, created.SubscriptionToken)
	if err != nil || subscription.UserName != user.DisplayName || subscription.NodeName != node.Name {
		t.Fatalf("Subscription() = %+v, %v", subscription, err)
	}
	if _, err := service.CreateAuthorization(ctx, input); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate CreateAuthorization() error = %v", err)
	}

	weekday := 1
	trafficLimit := int64(10 * 1024 * 1024)
	softLimit := 3
	expiresAt := fixed.Add(30 * 24 * time.Hour)
	input.Enabled = false
	input.TrafficLimitBytes = &trafficLimit
	input.Reset = ResetRule{Kind: "weekly", Value: &weekday, Timezone: "Asia/Shanghai"}
	input.ExpiresAt = &expiresAt
	input.SoftIPLimit = &softLimit
	updated, err := service.UpdateAuthorization(ctx, created.Authorization.ID, input)
	if err != nil {
		t.Fatalf("UpdateAuthorization() error = %v", err)
	}
	if updated.Enabled || updated.ResetValue == nil || *updated.ResetValue != 1 || updated.Timezone != "Asia/Shanghai" {
		t.Fatalf("updated authorization = %+v", updated)
	}

	rotated, err := service.RotateSubscriptionToken(ctx, updated.ID)
	if err != nil {
		t.Fatalf("RotateSubscriptionToken() error = %v", err)
	}
	if rotated.Token == created.SubscriptionToken || rotated.RotatedAt != fixed {
		t.Fatal("subscription token did not rotate")
	}
	if _, err := service.Subscription(ctx, created.SubscriptionToken); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Subscription() with rotated token error = %v", err)
	}
	stored, err := service.Authorization(ctx, updated.ID)
	if err != nil {
		t.Fatalf("Authorization() error = %v", err)
	}
	if string(stored.SubscriptionTokenHash) != string(auth.TokenHash(rotated.Token)) {
		t.Fatal("stored subscription token hash does not match rotated token")
	}

	if _, err := service.CreateServiceBinding(ctx, ServiceBindingInput{
		AuthorizationID: updated.ID, PluginID: "xray-runtime", ServiceID: "vless-main", Enabled: true,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateServiceBinding() without plugin service error = %v", err)
	}

	audit, err := service.ListAudit(ctx, 0, 200)
	if err != nil {
		t.Fatalf("ListAudit() error = %v", err)
	}
	actions := make(map[string]bool, len(audit))
	for _, entry := range audit {
		actions[entry.Action] = true
	}
	for _, action := range []string{
		"user.create", "node.create", "authorization.create", "authorization.update",
		"authorization.subscription_token.rotate",
	} {
		if !actions[action] {
			t.Errorf("audit does not contain %q", action)
		}
	}
	if actions["service_binding.create"] {
		t.Error("failed service binding creation produced a success audit entry")
	}
}

func TestAuthorizationRuleValidation(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	user, _ := service.CreateUser(ctx, UserInput{DisplayName: "Alice"})
	node, _ := service.CreateNode(ctx, NodeInput{Name: "Edge", Enabled: true})
	base := DefaultAuthorizationInput(user.ID, node.ID)

	tests := []struct {
		name  string
		input AuthorizationInput
		field string
	}{
		{name: "timezone", input: withReset(base, ResetRule{Kind: "daily", Timezone: "Local"}), field: "reset.timezone"},
		{name: "weekly value", input: withReset(base, ResetRule{Kind: "weekly", Timezone: "UTC"}), field: "reset.value"},
		{name: "interval anchor", input: withReset(base, ResetRule{Kind: "interval_days", Value: intPointer(7), Timezone: "UTC"}), field: "reset.period_anchor"},
		{name: "activity window", input: withActivityWindow(base, 10), field: "activity_window_seconds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.CreateAuthorization(ctx, test.input); fieldName(err) != test.field {
				t.Fatalf("CreateAuthorization() error = %v, want field %q", err, test.field)
			}
		})
	}
}

func withReset(input AuthorizationInput, rule ResetRule) AuthorizationInput {
	input.Reset = rule
	return input
}

func withActivityWindow(input AuthorizationInput, seconds int) AuthorizationInput {
	input.ActivityWindowSeconds = seconds
	return input
}

func intPointer(value int) *int {
	return &value
}
