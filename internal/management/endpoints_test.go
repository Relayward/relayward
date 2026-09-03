package management

import (
	"errors"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"

	"github.com/Relayward/relayward/internal/store"
)

func TestNodeEndpointsAndDNSProviderLifecycle(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()
	now := time.Date(2026, time.August, 30, 7, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	node, err := service.CreateNode(ctx, NodeInput{Name: "Edge", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := service.CreateNodeEndpoint(ctx, node.ID, NodeEndpointInput{
		DisplayName: "Direct IPv4", Kind: "direct", Enabled: true, SourceFamily: "ipv4",
		PublicPortOverrides: store.PublicPortOverrides{"io.relayward.test": {"vless-main": 45142}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.Available || direct.ResolvedAddress != "" || direct.Endpoint.SyncStatus != "not_applicable" {
		t.Fatalf("direct endpoint before observation = %+v", direct)
	}
	addresses := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.20"}}}
	if err := service.store.RecordNodePublicAddresses(ctx, node.ID, addresses, now, now); err != nil {
		t.Fatal(err)
	}
	listed, err := service.ListNodeEndpoints(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || !listed[0].Available || listed[0].ResolvedAddress != "203.0.113.20" {
		t.Fatalf("direct endpoint after observation = %+v", listed)
	}

	nat, err := service.CreateNodeEndpoint(ctx, node.ID, NodeEndpointInput{
		DisplayName: "NAT", Kind: "nat", Enabled: true, Address: " EDGE.EXAMPLE.COM ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !nat.Available || nat.ResolvedAddress != "edge.example.com" || nat.Endpoint.Address != "edge.example.com" {
		t.Fatalf("NAT endpoint = %+v", nat)
	}
	domain, err := service.CreateNodeEndpoint(ctx, node.ID, NodeEndpointInput{
		DisplayName: "Domain", Kind: "domain", Enabled: true, Address: "dynamic.example.net",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !domain.Available || domain.ResolvedAddress != "dynamic.example.net" {
		t.Fatalf("domain endpoint = %+v", domain)
	}

	token := "cloudflare-token-one"
	connection, err := service.CreateDNSProviderConnection(ctx, DNSProviderConnectionInput{
		Name: "Primary Cloudflare", Provider: "cloudflare", APIToken: &token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !connection.HasToken {
		t.Fatalf("created DNS provider connection = %+v", connection)
	}
	assertDNSProviderToken(t, service, connection.ID, token)
	connection, err = service.UpdateDNSProviderConnection(ctx, connection.ID, DNSProviderConnectionInput{Name: "Cloudflare", Provider: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if !connection.HasToken || connection.Provider != "cloudflare" || connection.Name != "Cloudflare" {
		t.Fatalf("updated DNS provider connection = %+v", connection)
	}
	assertDNSProviderToken(t, service, connection.ID, token)

	connectionID := connection.ID
	managed, err := service.CreateDDNSRecord(ctx, DDNSRecordInput{
		NodeID: node.ID, DisplayName: "Managed DDNS", Enabled: true, SourceFamily: "ipv4",
		DNSProviderConnectionID: &connectionID, ZoneName: "example.com", RecordName: "edge.example.com", TTL: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if managed.Available || managed.Endpoint.SyncStatus != "pending" {
		t.Fatalf("managed DDNS endpoint before sync = %+v", managed)
	}
	if _, err := service.UpdateNodeEndpoint(ctx, node.ID, managed.Endpoint.ID, NodeEndpointInput{
		DisplayName: "Domain", Kind: "domain", Enabled: true, Address: "edge.example.com",
	}); fieldName(err) != "kind" {
		t.Fatalf("node endpoint update of managed record error = %v", err)
	}
	if err := service.DeleteNodeEndpoint(ctx, node.ID, managed.Endpoint.ID); fieldName(err) != "endpoint_id" {
		t.Fatalf("node endpoint deletion of managed record error = %v", err)
	}
	if _, err := service.UpdateDDNSRecord(ctx, managed.Endpoint.ID, DDNSRecordInput{
		NodeID: node.ID, DisplayName: "Managed DDNS", Enabled: true, SourceFamily: "ipv4",
		DNSProviderConnectionID: &connectionID, ZoneName: "example.com", RecordName: "edge.example.com", TTL: 300, Proxied: true,
	}); fieldName(err) != "ttl" {
		t.Fatalf("proxied managed DDNS TTL error = %v", err)
	}
	otherNode, err := service.CreateNode(ctx, NodeInput{Name: "Other", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateDDNSRecord(ctx, managed.Endpoint.ID, DDNSRecordInput{
		NodeID: otherNode.ID, DisplayName: "Managed DDNS", Enabled: true, SourceFamily: "ipv4",
		DNSProviderConnectionID: &connectionID, ZoneName: "example.com", RecordName: "edge.example.com", TTL: 300,
	}); fieldName(err) != "node_id" {
		t.Fatalf("managed DDNS node change error = %v", err)
	}
	if err := service.DeleteDNSProviderConnection(ctx, connection.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("DeleteDNSProviderConnection() while referenced = %v", err)
	}

	if err := service.store.DeleteSecret(ctx, store.DNSProviderSecretOwnerType, connection.ID, store.DNSProviderTokenSecret); err != nil {
		t.Fatal(err)
	}
	connections, err := service.ListDNSProviderConnections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].HasToken {
		t.Fatalf("DNS provider connection after secret removal = %+v", connections)
	}
	replacement := "cloudflare-token-two"
	connection, err = service.UpdateDNSProviderConnection(ctx, connection.ID, DNSProviderConnectionInput{
		Name: connection.Name, Provider: connection.Provider, APIToken: &replacement,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertDNSProviderToken(t, service, connection.ID, replacement)
	records, err := service.ListDDNSRecords(ctx)
	if err != nil || len(records) != 1 || records[0].NodeName != node.Name {
		t.Fatalf("ListDDNSRecords() = %+v, %v", records, err)
	}
	if err := service.DeleteDDNSRecord(ctx, managed.Endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteDNSProviderConnection(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.Secret(ctx, store.DNSProviderSecretOwnerType, connection.ID, store.DNSProviderTokenSecret); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DNS provider secret after delete = %v", err)
	}
}

func TestNodeEndpointValidation(t *testing.T) {
	service := newTestService(t)
	node, err := service.CreateNode(t.Context(), NodeInput{Name: "Edge", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		input NodeEndpointInput
		field string
	}{
		"private NAT address": {
			input: NodeEndpointInput{DisplayName: "NAT", Kind: "nat", Enabled: true, Address: "192.168.1.10"},
			field: "address",
		},
		"invalid port override service": {
			input: NodeEndpointInput{DisplayName: "NAT", Kind: "nat", Enabled: true, Address: "edge.example.com", PublicPortOverrides: store.PublicPortOverrides{"io.relayward.test": {"BAD SERVICE": 443}}},
			field: "public_port_overrides",
		},
		"invalid port override plugin": {
			input: NodeEndpointInput{DisplayName: "NAT", Kind: "nat", Enabled: true, Address: "edge.example.com", PublicPortOverrides: store.PublicPortOverrides{"BAD PLUGIN": {"main": 443}}},
			field: "public_port_overrides",
		},
		"managed record belongs to global DDNS": {
			input: NodeEndpointInput{DisplayName: "DDNS", Kind: "managed_ddns", Enabled: true, SourceFamily: "ipv4"},
			field: "kind",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CreateNodeEndpoint(t.Context(), node.ID, test.input)
			if fieldName(err) != test.field {
				t.Fatalf("CreateNodeEndpoint() error = %v, field = %q", err, fieldName(err))
			}
		})
	}
}

func assertDNSProviderToken(t *testing.T, service *Service, connectionID, expected string) {
	t.Helper()
	ciphertext, err := service.store.Secret(t.Context(), store.DNSProviderSecretOwnerType, connectionID, store.DNSProviderTokenSecret)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := service.secrets.Decrypt(store.DNSProviderSecretOwnerType, connectionID, store.DNSProviderTokenSecret, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != expected {
		t.Fatalf("DNS provider token = %q", plaintext)
	}
}
