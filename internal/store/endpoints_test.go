package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

func TestRecordNodePublicAddressesKeepsNewestObservationAndMarksDDNSPending(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, time.August, 30, 6, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "Node", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	first := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.10"}}}
	if err := database.RecordNodePublicAddresses(ctx, "node-id", first, now, now); err != nil {
		t.Fatal(err)
	}
	connection := DNSProviderConnection{ID: "connection-id", Name: "Cloudflare", Provider: "cloudflare"}
	if err := database.CreateDNSProviderConnection(ctx, connection, []byte("ciphertext"), now); err != nil {
		t.Fatal(err)
	}
	connectionID := connection.ID
	endpoint := NodeEndpoint{
		ID: "endpoint-id", NodeID: "node-id", DisplayName: "Managed IPv4", Kind: "managed_ddns", Enabled: true,
		SourceFamily: "ipv4", PublicPortOverrides: PublicPortOverrides{},
		DNSProviderConnectionID: &connectionID, ZoneName: "example.com", RecordName: "edge.example.com",
		TTL: 1, SyncStatus: "synced", ActualAddress: "203.0.113.10",
	}
	if err := database.CreateNodeEndpoint(ctx, endpoint, now); err != nil {
		t.Fatal(err)
	}

	newer := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.11"}}}
	if err := database.RecordNodePublicAddresses(ctx, "node-id", newer, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.12"}}}
	if err := database.RecordNodePublicAddresses(ctx, "node-id", stale, now.Add(-time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	addresses, err := database.ListNodePublicAddresses(ctx, "node-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 1 || addresses[0].Address != "203.0.113.11" || !addresses[0].ObservedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("public addresses = %+v", addresses)
	}
	storedEndpoint, err := database.NodeEndpointByID(ctx, "node-id", endpoint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedEndpoint.SyncStatus != "pending" || storedEndpoint.SyncError != "" {
		t.Fatalf("managed endpoint after address change = %+v", storedEndpoint)
	}
}

func TestRecordNodePublicAddressesIgnoresDeletedNode(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Date(2026, time.August, 30, 6, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "Node", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteNode(ctx, "node-id", now); err != nil {
		t.Fatal(err)
	}
	event := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.10"}}}
	if err := database.RecordNodePublicAddresses(ctx, "node-id", event, now, now); err != nil {
		t.Fatalf("RecordNodePublicAddresses() after node deletion = %v", err)
	}
}
