package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

func TestNodeEndpointAndDNSProviderAPI(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	nodeRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`), headers, sessionCookie)
	var node nodeResponse
	decodeResponse(t, nodeRequest, &node)
	if nodeRequest.Code != http.StatusCreated {
		t.Fatalf("create node = %d, %s", nodeRequest.Code, nodeRequest.Body.String())
	}

	const token = "cloudflare-api-token"
	providerRequest := performRequest(handler, http.MethodPost, "/api/v1/dns-provider-connections",
		[]byte(`{"name":"Cloudflare","provider":"cloudflare","api_token":"`+token+`"}`), headers, sessionCookie)
	if providerRequest.Code != http.StatusCreated || strings.Contains(providerRequest.Body.String(), token) {
		t.Fatalf("create DNS provider = %d, %s", providerRequest.Code, providerRequest.Body.String())
	}
	var provider dnsProviderConnectionResponse
	decodeResponse(t, providerRequest, &provider)
	if provider.ID == "" || !provider.HasToken || provider.Provider != "cloudflare" {
		t.Fatalf("DNS provider response = %+v", provider)
	}

	now := time.Date(2026, time.August, 30, 9, 0, 0, 0, time.UTC)
	addresses := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.40"}}}
	if err := database.RecordNodePublicAddresses(t.Context(), node.ID, addresses, now, now); err != nil {
		t.Fatal(err)
	}
	publicAddresses := performRequest(handler, http.MethodGet, "/api/v1/nodes/"+node.ID+"/public-addresses", nil, nil, sessionCookie)
	if publicAddresses.Code != http.StatusOK || !strings.Contains(publicAddresses.Body.String(), "203.0.113.40") {
		t.Fatalf("public addresses = %d, %s", publicAddresses.Code, publicAddresses.Body.String())
	}

	directRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+node.ID+"/endpoints",
		[]byte(`{"display_name":"Direct IPv4","kind":"direct","enabled":true,"source_family":"ipv4","public_port_overrides":{"io.relayward.test":{"vless-main":45142}}}`),
		headers, sessionCookie)
	if directRequest.Code != http.StatusCreated {
		t.Fatalf("create direct endpoint = %d, %s", directRequest.Code, directRequest.Body.String())
	}
	var direct nodeEndpointResponse
	decodeResponse(t, directRequest, &direct)
	if !direct.Available || direct.ResolvedAddress != "203.0.113.40" || direct.PublicPortOverrides["io.relayward.test"]["vless-main"] != 45142 {
		t.Fatalf("direct endpoint response = %+v", direct)
	}

	managedBody := fmt.Sprintf(`{
      "node_id":%q,"display_name":"Managed IPv4","enabled":true,"source_family":"ipv4",
      "dns_provider_connection_id":%q,"zone_name":"example.com",
      "record_name":"edge.example.com","ttl":300,"proxied":false
    }`, node.ID, provider.ID)
	managedRequest := performRequest(handler, http.MethodPost, "/api/v1/ddns-records",
		[]byte(managedBody), headers, sessionCookie)
	if managedRequest.Code != http.StatusCreated {
		t.Fatalf("create managed endpoint = %d, %s", managedRequest.Code, managedRequest.Body.String())
	}
	var managed nodeEndpointResponse
	decodeResponse(t, managedRequest, &managed)
	if managed.Available || managed.Kind != "managed_ddns" || managed.SyncStatus != "pending" || managed.DNSProviderConnectionID == nil {
		t.Fatalf("managed endpoint response = %+v", managed)
	}
	managedFromNode := performRequest(handler, http.MethodDelete, "/api/v1/nodes/"+node.ID+"/endpoints/"+managed.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if managedFromNode.Code != http.StatusBadRequest {
		t.Fatalf("delete managed record through node endpoint = %d, %s", managedFromNode.Code, managedFromNode.Body.String())
	}
	managedList := performRequest(handler, http.MethodGet, "/api/v1/ddns-records", nil, nil, sessionCookie)
	if managedList.Code != http.StatusOK || !strings.Contains(managedList.Body.String(), `"node_name":"Edge"`) {
		t.Fatalf("list DDNS records = %d, %s", managedList.Code, managedList.Body.String())
	}

	listRequest := performRequest(handler, http.MethodGet, "/api/v1/nodes/"+node.ID+"/endpoints", nil, nil, sessionCookie)
	if listRequest.Code != http.StatusOK || !strings.Contains(listRequest.Body.String(), "Direct IPv4") || !strings.Contains(listRequest.Body.String(), "Managed IPv4") {
		t.Fatalf("list endpoints = %d, %s", listRequest.Code, listRequest.Body.String())
	}
	conflict := performRequest(handler, http.MethodDelete, "/api/v1/dns-provider-connections/"+provider.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("delete referenced provider = %d, %s", conflict.Code, conflict.Body.String())
	}
	deleteEndpoint := performRequest(handler, http.MethodDelete, "/api/v1/ddns-records/"+managed.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if deleteEndpoint.Code != http.StatusNoContent {
		t.Fatalf("delete endpoint = %d, %s", deleteEndpoint.Code, deleteEndpoint.Body.String())
	}
	deleteProvider := performRequest(handler, http.MethodDelete, "/api/v1/dns-provider-connections/"+provider.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if deleteProvider.Code != http.StatusNoContent {
		t.Fatalf("delete provider = %d, %s", deleteProvider.Code, deleteProvider.Body.String())
	}
}
