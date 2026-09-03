package ddns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

type providerStub struct {
	calls   int
	token   string
	address string
	err     error
}

func (provider *providerStub) Sync(_ context.Context, token string, _ store.NodeEndpoint, address string) error {
	provider.calls++
	provider.token = token
	provider.address = address
	return provider.err
}

func TestReconcilerWaitsSyncsSkipsAndRecordsFailure(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	database, err := store.Open(ctx, filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, store.Node{ID: "node-id", Name: "Node", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	connection := store.DNSProviderConnection{ID: "connection-id", Name: "Cloudflare", Provider: "cloudflare"}
	ciphertext, err := secrets.Encrypt(store.DNSProviderSecretOwnerType, connection.ID, store.DNSProviderTokenSecret, []byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateDNSProviderConnection(ctx, connection, ciphertext, now); err != nil {
		t.Fatal(err)
	}
	connectionID := connection.ID
	endpoint := store.NodeEndpoint{
		ID: "endpoint-id", NodeID: "node-id", DisplayName: "Managed IPv4", Kind: "managed_ddns", Enabled: true,
		SourceFamily: "ipv4", PublicPortOverrides: store.PublicPortOverrides{},
		DNSProviderConnectionID: &connectionID, ZoneName: "example.com", RecordName: "edge.example.com",
		TTL: 300, SyncStatus: "pending",
	}
	if err := database.CreateNodeEndpoint(ctx, endpoint, now); err != nil {
		t.Fatal(err)
	}
	provider := &providerStub{}
	reconciler := &Reconciler{
		store: database, secrets: secrets, provider: provider,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now },
	}

	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored := readEndpoint(t, database, endpoint.NodeID, endpoint.ID)
	if provider.calls != 0 || stored.SyncStatus != "pending" || stored.SyncError == "" {
		t.Fatalf("endpoint while waiting = %+v, provider calls = %d", stored, provider.calls)
	}

	observation := agentv1.PublicAddressesEvent{Addresses: []agentv1.PublicAddressObservation{{Family: "ipv4", Address: "203.0.113.30"}}}
	if err := database.RecordNodePublicAddresses(ctx, endpoint.NodeID, observation, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := reconciler.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored = readEndpoint(t, database, endpoint.NodeID, endpoint.ID)
	if provider.calls != 1 || provider.token != "test-token" || provider.address != "203.0.113.30" ||
		stored.SyncStatus != "synced" || stored.ActualAddress != "203.0.113.30" || stored.SyncError != "" || stored.SyncedAt == nil {
		t.Fatalf("synced endpoint = %+v, provider = %+v", stored, provider)
	}
	if err := reconciler.RunOnce(ctx); err != nil || provider.calls != 1 {
		t.Fatalf("unchanged RunOnce() error = %v, provider calls = %d", err, provider.calls)
	}

	provider.err = errors.New("provider failed")
	observation.Addresses[0].Address = "203.0.113.31"
	if err := database.RecordNodePublicAddresses(ctx, endpoint.NodeID, observation, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := reconciler.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce() error = nil after provider failure")
	}
	stored = readEndpoint(t, database, endpoint.NodeID, endpoint.ID)
	if provider.calls != 2 || stored.SyncStatus != "failed" || stored.ActualAddress != "203.0.113.30" || stored.SyncError != "provider failed" {
		t.Fatalf("failed endpoint = %+v, provider calls = %d", stored, provider.calls)
	}
}

func readEndpoint(t *testing.T, database *store.Store, nodeID, endpointID string) store.NodeEndpoint {
	t.Helper()
	value, err := database.NodeEndpointByID(t.Context(), nodeID, endpointID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
