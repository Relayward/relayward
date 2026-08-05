package server

import (
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	nodepluginv1 "github.com/Relayward/relayward-sdk/nodeplugin/v1"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

func TestSubscriptionHTTPFormatsAnnouncementAndInactiveGate(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(t.Context(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	events, err := eventstore.Open(t.Context(), filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.NewService(database, secrets)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &serverPluginRuntime{}
	releases := &serverReleaseClient{release: serverPluginRelease("1.2.3")}
	manager := management.NewService(database, secrets)
	if err := manager.ConfigurePluginLifecycle(releases, &serverArtifactStore{}, runtime); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	node, err := manager.CreateNode(t.Context(), management.NodeInput{Name: "Edge", PublicAddress: "edge.example.com", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := manager.CreateRegistrationToken(t.Context(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := manager.RegisterAgent(t.Context(), agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: registration.Token, AgentVersion: "0.1.0",
		Hostname: "edge", OS: "linux", Arch: "amd64",
		Capabilities: []string{agentv1.CapabilityControlCommands, agentv1.CapabilityPluginSupervision},
	})
	if err != nil {
		t.Fatal(err)
	}
	installation, err := manager.InstallPluginRelease(t.Context(), management.PluginReleaseInput{
		Repository: releases.release.Repository.URL(), Version: "1.2.3", ApprovedPermissions: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := manager.ReconcileNodePlugin(t.Context(), registered.Node.ID, installation.PluginID, management.NodePluginInput{
		DesiredState: agentv1.PluginStateRunning, Version: installation.ActiveVersion,
		Configuration: []byte(`{"enabled":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordNodePluginStatus(t.Context(), registered.Node.ID, agentv1.PluginStatusEvent{
		PluginID: installation.PluginID, Generation: instance.Generation, State: agentv1.PluginStateRunning,
		Version: installation.ActiveVersion, ConfigurationSHA256: instance.DesiredConfigurationSHA256,
		Health:       agentv1.PluginHealthHealthy,
		Capabilities: []string{nodepluginv1.CapabilityServiceControl, nodepluginv1.CapabilityTrafficCounters},
	}, now, now); err != nil {
		t.Fatal(err)
	}
	if err := database.ReplacePluginServices(t.Context(), installation.PluginID, registered.Node.ID, []store.PluginService{{
		ServiceID: "main", DisplayName: "Main", Enabled: true,
		Capabilities: []string{"subscription.render"}, SubscriptionSHA256: strings.Repeat("a", 64),
	}}, now); err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), management.UserInput{DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateAuthorization(t.Context(), management.DefaultAuthorizationInput(user.ID, registered.Node.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateServiceBinding(t.Context(), management.ServiceBindingInput{
		AuthorizationID: created.Authorization.ID, PluginID: installation.PluginID, ServiceID: "main", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(Options{
		Version: "test", Store: database, EventStore: events, Auth: authentication, Management: manager,
		Secrets: secrets, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	announcement := performRequest(handler, http.MethodPut, "/api/v1/announcement", []byte(`{"content":"Maintenance tonight"}`), headers, sessionCookie)
	if announcement.Code != http.StatusOK {
		t.Fatalf("announcement status = %d, body = %s", announcement.Code, announcement.Body.String())
	}
	settings := performRequest(handler, http.MethodPut, "/api/v1/settings", []byte(`{
      "session_lifetime_minutes":1440,"timezone":"UTC","public_url":"https://panel.example.com",
      "subscription_title":"Relayward Home","support_url":"https://support.example.com",
      "profile_url":"https://example.com/account","subscription_refresh_hours":24
    }`), headers, sessionCookie)
	if settings.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body = %s", settings.Code, settings.Body.String())
	}
	catalog := performRequest(handler, http.MethodGet, "/api/v1/plugin-services?node_id="+registered.Node.ID, nil, nil, sessionCookie)
	if catalog.Code != http.StatusOK || !strings.Contains(catalog.Body.String(), `"display_name":"Main"`) || strings.Contains(catalog.Body.String(), "subscription_sha256") {
		t.Fatalf("plugin service catalog status = %d, body = %s", catalog.Code, catalog.Body.String())
	}

	root := "/api/v1/subscriptions/" + created.SubscriptionToken
	metadata := performRequest(handler, http.MethodGet, root, nil, nil)
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"status":"active"`) ||
		!strings.Contains(metadata.Body.String(), `"title":"Relayward Home"`) ||
		!strings.Contains(metadata.Body.String(), `"support_url":"https://support.example.com"`) ||
		!strings.Contains(metadata.Body.String(), `"announcement":"Maintenance tonight"`) ||
		!strings.Contains(metadata.Body.String(), `"display_name":"Main"`) {
		t.Fatalf("subscription metadata status = %d, body = %s", metadata.Code, metadata.Body.String())
	}
	base64Response := performRequest(handler, http.MethodGet, root+"/base64", nil, nil)
	decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(base64Response.Body.String()))
	if base64Response.Code != http.StatusOK || decodeErr != nil || !strings.Contains(string(decoded), "relayward-test://") ||
		!strings.Contains(base64Response.Header().Get("Content-Disposition"), "relayward.txt") ||
		base64Response.Header().Get("Profile-Update-Interval") != "24" ||
		base64Response.Header().Get("Support-URL") != "https://support.example.com" {
		t.Fatalf("base64 status = %d, content=%q, decoded=%q, error=%v", base64Response.Code, base64Response.Body.String(), decoded, decodeErr)
	}
	mihomo := performRequest(handler, http.MethodGet, root+"/mihomo.yaml", nil, nil)
	singBox := performRequest(handler, http.MethodGet, root+"/sing-box.json", nil, nil)
	if mihomo.Code != http.StatusOK || !strings.Contains(mihomo.Body.String(), "proxies:") ||
		singBox.Code != http.StatusOK || !strings.Contains(singBox.Body.String(), `"outbounds"`) || runtime.renderCalls != 1 {
		t.Fatalf("format responses: mihomo=%d %q sing-box=%d %q calls=%d", mihomo.Code, mihomo.Body.String(), singBox.Code, singBox.Body.String(), runtime.renderCalls)
	}

	disabled := management.DefaultAuthorizationInput(user.ID, registered.Node.ID)
	disabled.Enabled = false
	if _, err := manager.UpdateAuthorization(t.Context(), created.Authorization.ID, disabled); err != nil {
		t.Fatal(err)
	}
	blocked := performRequest(handler, http.MethodGet, root+"/base64", nil, nil)
	if blocked.Code != http.StatusForbidden || strings.Contains(blocked.Body.String(), "relayward-test://") {
		t.Fatalf("disabled subscription status = %d, body = %s", blocked.Code, blocked.Body.String())
	}
	rotated, err := manager.RotateSubscriptionToken(t.Context(), created.Authorization.ID)
	if err != nil || rotated.Token == created.SubscriptionToken {
		t.Fatal(err)
	}
	missing := performRequest(handler, http.MethodGet, root, nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("rotated subscription status = %d", missing.Code)
	}
}
