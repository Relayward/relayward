package management

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUpdateSystemSettingsValidatesAndPersists(t *testing.T) {
	service := newTestService(t)
	fixed := time.Date(2026, time.August, 5, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }
	ctx := context.Background()

	value, err := service.UpdateSystemSettings(ctx, SystemSettingsInput{
		SessionLifetimeMinutes: 180, Timezone: "Asia/Shanghai", PublicURL: "https://panel.example.com/",
		SubscriptionTitle: "Relayward Home", SupportURL: "https://support.example.com/help",
		ProfileURL: "https://example.com/account", SubscriptionRefreshHours: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.PublicURL != "https://panel.example.com" || value.Timezone != "Asia/Shanghai" ||
		value.SubscriptionTitle != "Relayward Home" || !value.UpdatedAt.Equal(fixed) {
		t.Fatalf("settings = %+v", value)
	}
	stored, err := service.SystemSettings(ctx)
	if err != nil || stored != value {
		t.Fatalf("stored settings = %+v, %v", stored, err)
	}

	invalidInputs := []SystemSettingsInput{
		{SessionLifetimeMinutes: 59, Timezone: "UTC", SubscriptionTitle: "Relayward"},
		{SessionLifetimeMinutes: 60, Timezone: "Not/AZone", SubscriptionTitle: "Relayward"},
		{SessionLifetimeMinutes: 60, Timezone: "UTC", PublicURL: "http://panel.example.com", SubscriptionTitle: "Relayward"},
		{SessionLifetimeMinutes: 60, Timezone: "UTC", PublicURL: "https://panel.example.com/path", SubscriptionTitle: "Relayward"},
		{SessionLifetimeMinutes: 60, Timezone: "UTC", SubscriptionTitle: ""},
	}
	for _, input := range invalidInputs {
		var fieldError *FieldError
		if _, err := service.UpdateSystemSettings(ctx, input); !errors.As(err, &fieldError) {
			t.Fatalf("UpdateSystemSettings(%+v) error = %v", input, err)
		}
	}
}
