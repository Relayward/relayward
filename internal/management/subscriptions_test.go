package management

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	nodepluginv1 "github.com/Relayward/relayward-sdk/nodeplugin/v1"

	"github.com/Relayward/relayward/internal/store"
)

type subscriptionRuntimeStub struct {
	calls int
	err   error
}

func (*subscriptionRuntimeStub) Switch(context.Context, store.PluginVersion) error { return nil }
func (*subscriptionRuntimeStub) Rollback(context.Context, string, *store.PluginVersion) error {
	return nil
}
func (*subscriptionRuntimeStub) StopPlugin(context.Context, string) error { return nil }
func (*subscriptionRuntimeStub) InvokeUI(context.Context, string, string, []byte) ([]byte, error) {
	return []byte(`{}`), nil
}

func (runtime *subscriptionRuntimeStub) RenderSubscription(_ context.Context, _ string,
	request *centerpluginv1.RenderSubscriptionRequest,
) (*centerpluginv1.RenderSubscriptionResponse, error) {
	runtime.calls++
	if runtime.err != nil {
		return nil, runtime.err
	}
	services := make([]*centerpluginv1.SubscriptionServiceContribution, len(request.Services))
	for index, service := range request.Services {
		services[index] = &centerpluginv1.SubscriptionServiceContribution{
			ServiceId: service.ServiceId, DisplayName: service.DisplayName,
			Uris:                 []string{"relayward-test://credential@edge.example.com:443#Edge"},
			MihomoProxiesJson:    [][]byte{[]byte(`{"name":"Edge","port":443,"server":"edge.example.com","type":"test"}`)},
			SingBoxOutboundsJson: [][]byte{[]byte(`{"server":"edge.example.com","server_port":443,"tag":"Edge","type":"test"}`)},
		}
	}
	return &centerpluginv1.RenderSubscriptionResponse{Services: services}, nil
}

func TestSubscriptionRenderingCacheAndInputIsolation(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	runtime := &subscriptionRuntimeStub{}
	service.pluginRuntime = runtime

	node := registerManagedAgent(t, service, "Edge", []string{
		agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision,
	})
	node, err := service.UpdateNode(ctx, node.ID, NodeInput{Name: "Edge", PublicAddress: "edge.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.CreateUser(ctx, UserInput{DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateAuthorization(ctx, DefaultAuthorizationInput(user.ID, node.ID))
	if err != nil {
		t.Fatal(err)
	}
	pluginManifest := managedRuntimeManifest()
	if err := service.store.CreatePluginInstallation(ctx, store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version, ActiveVersion: pluginManifest.Version,
		Manifest: pluginManifest, State: "active", Health: "healthy",
	}, now); err != nil {
		t.Fatal(err)
	}
	service.pluginReleases = &releaseClientStub{resolvedURL: "https://release-assets.githubusercontent.com/plugin"}
	instance, err := service.ReconcileNodePlugin(ctx, node.ID, pluginManifest.ID, NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: pluginManifest.Version,
		Configuration: []byte(`{"enabled":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordNodePluginStatus(ctx, node.ID, agentv1.PluginStatusEvent{
		PluginID: pluginManifest.ID, Generation: instance.Generation, State: agentv1.PluginStateRunning,
		Version: pluginManifest.Version, ConfigurationSHA256: instance.DesiredConfigurationSHA256,
		Health:       agentv1.PluginHealthHealthy,
		Capabilities: []string{nodepluginv1.CapabilityServiceControl, nodepluginv1.CapabilityTrafficCounters},
	}, now.Add(time.Second), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	services := []store.PluginService{
		{ServiceID: "backup", DisplayName: "Backup", Enabled: true, Capabilities: []string{"subscription.render"}, SubscriptionSHA256: strings.Repeat("a", 64)},
		{ServiceID: "main", DisplayName: "Main", Enabled: true, Capabilities: []string{"subscription.render"}, SubscriptionSHA256: strings.Repeat("b", 64)},
	}
	if err := service.store.ReplacePluginServices(ctx, pluginManifest.ID, node.ID, services, now); err != nil {
		t.Fatal(err)
	}
	for _, value := range services {
		if _, err := service.CreateServiceBinding(ctx, ServiceBindingInput{
			AuthorizationID: created.Authorization.ID, PluginID: pluginManifest.ID, ServiceID: value.ServiceID, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	base64Result, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatBase64)
	if err != nil || base64Result.Cached || runtime.calls != 1 {
		t.Fatalf("first RenderSubscription() = %+v, calls=%d, error=%v", base64Result, runtime.calls, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(base64Result.Content)))
	if err != nil || string(decoded) != "relayward-test://credential@edge.example.com:443#Edge" {
		t.Fatalf("base64 subscription = %q, decode error=%v", decoded, err)
	}
	mihomo, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatMihomo)
	if err != nil || !mihomo.Cached || runtime.calls != 1 || strings.Count(string(mihomo.Content), "name: Edge") != 1 {
		t.Fatalf("cached Mihomo subscription = %q, calls=%d, error=%v", mihomo.Content, runtime.calls, err)
	}

	now = now.Add(6 * time.Minute)
	runtime.err = errors.New("plugin stopped")
	fallback, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatSingBox)
	if err != nil || !fallback.Cached || runtime.calls != 2 || strings.Count(string(fallback.Content), `"tag": "Edge"`) != 1 {
		t.Fatalf("fallback subscription = %q, calls=%d, error=%v", fallback.Content, runtime.calls, err)
	}
	if err := service.store.RecordPluginRuntimeStatus(ctx, pluginManifest.ID, "failed", "unhealthy", 1, nil, now); err != nil {
		t.Fatal(err)
	}
	centerFailureFallback, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatBase64)
	if err != nil || !centerFailureFallback.Cached || runtime.calls != 2 {
		t.Fatalf("center failure fallback = %+v, calls=%d, error=%v", centerFailureFallback, runtime.calls, err)
	}

	now = now.Add(time.Minute)
	services[1].SubscriptionSHA256 = strings.Repeat("d", 64)
	if err := service.store.ReplacePluginServices(ctx, pluginManifest.ID, node.ID, services, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatBase64); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("RenderSubscription() reused cache after input change: %v", err)
	}

	disabled := DefaultAuthorizationInput(user.ID, node.ID)
	disabled.Enabled = false
	if _, err := service.UpdateAuthorization(ctx, created.Authorization.ID, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RenderSubscription(ctx, created.SubscriptionToken, store.SubscriptionFormatBase64); !errors.Is(err, ErrSubscriptionInactive) {
		t.Fatalf("disabled RenderSubscription() error = %v", err)
	}
}

func TestSubscriptionStatusIncludesQuota(t *testing.T) {
	limit := int64(1024)
	used := uint64(1024)
	value := store.SubscriptionSnapshot{
		NodeEnabled: true, Authorization: store.Authorization{Enabled: true, TrafficLimitBytes: &limit},
		TrafficUsedBytes: &used,
	}
	if status := SubscriptionStatus(value, time.Now()); status != "quota_exceeded" {
		t.Fatalf("SubscriptionStatus() = %q", status)
	}
}

func TestAggregateSubscriptionRejectsCombinedPluginOverflow(t *testing.T) {
	fragment := []byte(`{"name":"` + strings.Repeat("a", 64<<10) + `"}`)
	services := make([]*centerpluginv1.SubscriptionServiceContribution, 9)
	for index := range services {
		services[index] = &centerpluginv1.SubscriptionServiceContribution{
			ServiceId:         fmt.Sprintf("service-%d", index),
			MihomoProxiesJson: [][]byte{fragment},
		}
	}
	if _, _, _, err := aggregateSubscription(services); err == nil {
		t.Fatal("aggregateSubscription() accepted fragments over the center-wide size limit")
	}
}
