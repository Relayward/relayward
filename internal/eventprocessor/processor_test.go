package eventprocessor

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	policyv1 "github.com/Relayward/relayward-sdk/policy/v1"
	"github.com/klauspost/compress/zstd"

	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/store"
)

const (
	processorNodeID          = "10000000-0000-4000-8000-000000000001"
	processorUserID          = "20000000-0000-4000-8000-000000000002"
	processorAuthorizationID = "30000000-0000-4000-8000-000000000003"
	processorStreamID        = "0123456789abcdef0123456789abcdef"
)

func TestProcessorConsumesTrafficAndPolicyIndependently(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	business, err := store.Open(ctx, filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatalf("open business store: %v", err)
	}
	defer business.Close()
	events, err := eventstore.Open(ctx, filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatalf("open event store: %v", err)
	}
	defer events.Close()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	prepareProcessorAuthorization(t, business, now)
	period, err := policyv1.CurrentPeriod(policyv1.ResetRule{Kind: policyv1.ResetDaily, Timezone: "UTC"}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("CurrentPeriod() error = %v", err)
	}
	traffic := agentv1.TrafficSnapshotEvent{
		AuthorizationID: processorAuthorizationID, Period: period, Revision: 3,
		UploadBytes: 120, DownloadBytes: 240,
	}
	status := agentv1.PolicyStatusEvent{
		Generation: 2, AuthorizationID: processorAuthorizationID, Period: period,
		UploadBytes: 120, DownloadBytes: 240, ServicesEnabled: true,
		Reason: agentv1.PolicyReasonActive, ActiveIPCount: 2,
	}
	first, _ := agentv1.NewEvent(processorNodeID, processorStreamID, 1, agentv1.EventTrafficSnapshot, now, traffic)
	second, _ := agentv1.NewEvent(processorNodeID, processorStreamID, 2, agentv1.EventPolicyStatus, now, status)
	batch := agentv1.EventBatch{
		APIVersion: agentv1.APIVersion, NodeID: processorNodeID, StreamID: processorStreamID,
		FirstSequence: 1, LastSequence: 2, Events: []agentv1.Event{first, second},
	}
	if _, err := events.Ingest(ctx, processorNodeID, batch, now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	processor, err := New(events, business, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	processor.now = func() time.Time { return now.Add(time.Minute) }
	if err := processor.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	periods, err := business.TrafficPeriods(ctx, processorAuthorizationID, 10)
	if err != nil || len(periods) != 1 || periods[0].Revision != 3 || periods[0].DownloadBytes != 240 {
		t.Fatalf("traffic periods = %+v, %v", periods, err)
	}
	storedStatus, err := business.AuthorizationPolicyStatusByID(ctx, processorAuthorizationID)
	if err != nil || storedStatus.Generation != 2 || storedStatus.ActiveIPCount != 2 {
		t.Fatalf("policy status = %+v, %v", storedStatus, err)
	}
	for _, consumerID := range ConsumerIDs {
		state, err := events.ConsumerState(ctx, consumerID)
		if err != nil || state.LastEventRowID != 2 || state.ConsecutiveFailures != 0 {
			t.Fatalf("consumer %s state = %+v, %v", consumerID, state, err)
		}
	}
	if err := processor.RunOnce(ctx); err != nil {
		t.Fatalf("idempotent RunOnce() error = %v", err)
	}
}

func TestProcessorRecordsConsumerFailureWithoutBlockingOtherCursors(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	business, err := store.Open(ctx, filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatalf("open business store: %v", err)
	}
	defer business.Close()
	events, err := eventstore.Open(ctx, filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatalf("open event store: %v", err)
	}
	defer events.Close()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	invalidAccess, err := agentv1.NewEvent(processorNodeID, processorStreamID, 1, agentv1.EventAccess, now, map[string]bool{"invalid": true})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if _, err := events.Ingest(ctx, processorNodeID, agentv1.EventBatch{
		APIVersion: agentv1.APIVersion, NodeID: processorNodeID, StreamID: processorStreamID,
		FirstSequence: 1, LastSequence: 1, Events: []agentv1.Event{invalidAccess},
	}, now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	processor, err := New(events, business, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	processor.now = func() time.Time { return now.Add(time.Minute) }
	if err := processor.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce() accepted an invalid access event")
	}
	for _, consumerID := range []string{TrafficConsumerID, PolicyConsumerID} {
		state, err := events.ConsumerState(ctx, consumerID)
		if err != nil || state.LastEventRowID != 1 || state.ConsecutiveFailures != 0 {
			t.Fatalf("unrelated consumer %s state = %+v, %v", consumerID, state, err)
		}
	}
	failed, err := events.ConsumerState(ctx, AccessConsumerID)
	if err != nil || failed.LastEventRowID != 0 || failed.ConsecutiveFailures != 1 ||
		failed.FailedEventRowID == nil || *failed.FailedEventRowID != 1 || failed.RetryAfter == nil {
		t.Fatalf("failed access consumer state = %+v, %v", failed, err)
	}
	if err := processor.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() during retry delay error = %v", err)
	}
	unchanged, _ := events.ConsumerState(ctx, AccessConsumerID)
	if unchanged.ConsecutiveFailures != 1 {
		t.Fatalf("access consumer retried before delay = %+v", unchanged)
	}
	processor.now = func() time.Time { return now.Add(time.Minute + consumerRetryDelay) }
	if err := processor.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce() retry accepted an invalid access event")
	}
	retried, _ := events.ConsumerState(ctx, AccessConsumerID)
	if retried.ConsecutiveFailures != 2 || retried.FailedEventRowID == nil || *retried.FailedEventRowID != 1 {
		t.Fatalf("retried access consumer state = %+v", retried)
	}
}

func TestArchiverWritesZstandardJSONLAndPrunesHotAccess(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	events, err := eventstore.Open(ctx, filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatalf("open event store: %v", err)
	}
	defer events.Close()
	observedAt := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	access := agentv1.AccessEvent{
		SourceStreamID: "11111111111111111111111111111111", SourceEventID: "event-1",
		PluginID: "io.relayward.test", ServiceID: "main", AuthorizationID: processorAuthorizationID,
		SourceIP: "203.0.113.10", Destination: "example.com", DestinationPort: 443,
		Network: "tcp", Protocol: "tls", Action: agentv1.AccessActionAccepted,
	}
	sourceEvent, _ := agentv1.NewEvent(processorNodeID, processorStreamID, 1, agentv1.EventAccess, observedAt, access)
	if err := events.StoreAccessEvent(ctx, eventstore.StoredEvent{
		RowID: 1, NodeID: processorNodeID, StreamID: processorStreamID,
		Event: sourceEvent, ReceivedAt: observedAt,
	}, access); err != nil {
		t.Fatalf("StoreAccessEvent() error = %v", err)
	}
	for _, consumerID := range ConsumerIDs {
		if err := events.EnsureConsumer(ctx, consumerID, observedAt); err != nil {
			t.Fatalf("EnsureConsumer(%s) error = %v", consumerID, err)
		}
		if err := events.AdvanceConsumer(ctx, consumerID, 1, observedAt); err != nil {
			t.Fatalf("AdvanceConsumer(%s) error = %v", consumerID, err)
		}
	}
	archiveDirectory := filepath.Join(directory, "archive")
	archiver, err := NewArchiver(events, slog.New(slog.NewTextHandler(io.Discard, nil)), ArchiveOptions{
		Directory: archiveDirectory, HotRetention: time.Hour, ArchiveRetention: 48 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewArchiver() error = %v", err)
	}
	archiver.now = func() time.Time { return time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC) }
	if err := archiver.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	archivePath := filepath.Join(archiveDirectory, "access", "2026", "08", "2026-08-01.jsonl.zst")
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	compressed, err := zstd.NewReader(file)
	if err != nil {
		file.Close()
		t.Fatalf("open Zstandard archive: %v", err)
	}
	scanner := bufio.NewScanner(compressed)
	if !scanner.Scan() {
		t.Fatalf("archive has no record: %v", scanner.Err())
	}
	var archived eventstore.AccessRecord
	if err := json.Unmarshal(scanner.Bytes(), &archived); err != nil {
		t.Fatalf("decode archived record: %v", err)
	}
	if scanner.Scan() || scanner.Err() != nil {
		t.Fatalf("archive contains unexpected records or error: %v", scanner.Err())
	}
	compressed.Close()
	file.Close()
	if archived.SourceIP != access.SourceIP || archived.Destination != access.Destination {
		t.Fatalf("archived access record = %+v", archived)
	}
	recent, err := events.RecentAccessEvents(ctx, "", 0, 10)
	if err != nil || len(recent) != 0 {
		t.Fatalf("hot access after pruning = %+v, %v", recent, err)
	}
	archives, err := events.AccessArchivesBefore(ctx, "2026-08-03")
	if err != nil || len(archives) != 1 || archives[0].EventCount != 1 || archives[0].SHA256 == "" {
		t.Fatalf("archive metadata = %+v, %v", archives, err)
	}
	late := access
	late.SourceEventID = "event-2"
	late.Destination = "late.example"
	lateEvent, _ := agentv1.NewEvent(processorNodeID, processorStreamID, 2, agentv1.EventAccess, observedAt.Add(time.Hour), late)
	if err := events.StoreAccessEvent(ctx, eventstore.StoredEvent{
		RowID: 2, NodeID: processorNodeID, StreamID: processorStreamID,
		Event: lateEvent, ReceivedAt: observedAt.Add(26 * time.Hour),
	}, late); err != nil {
		t.Fatalf("StoreAccessEvent() late error = %v", err)
	}
	if err := archiver.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() with late event error = %v", err)
	}
	archivedValues := readAccessArchive(t, archivePath)
	if len(archivedValues) != 2 || archivedValues[0].Destination != access.Destination || archivedValues[1].Destination != late.Destination {
		t.Fatalf("archive after late event = %+v", archivedValues)
	}
	archives, err = events.AccessArchivesBefore(ctx, "2026-08-03")
	if err != nil || len(archives) != 1 || archives[0].EventCount != 2 || archives[0].MaxAccessID != 2 {
		t.Fatalf("archive metadata after late event = %+v, %v", archives, err)
	}
}

func readAccessArchive(t *testing.T, path string) []eventstore.AccessRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open access archive: %v", err)
	}
	defer file.Close()
	compressed, err := zstd.NewReader(file)
	if err != nil {
		t.Fatalf("open Zstandard access archive: %v", err)
	}
	defer compressed.Close()
	decoder := json.NewDecoder(compressed)
	values := make([]eventstore.AccessRecord, 0)
	for {
		var value eventstore.AccessRecord
		if err := decoder.Decode(&value); err == io.EOF {
			return values
		} else if err != nil {
			t.Fatalf("decode access archive: %v", err)
		}
		values = append(values, value)
	}
}

func prepareProcessorAuthorization(t *testing.T, business *store.Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := business.CreateNode(ctx, store.Node{ID: processorNodeID, Name: "edge", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if err := business.CreateUser(ctx, store.User{ID: processorUserID, DisplayName: "user"}, now); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := business.CreateAuthorization(ctx, store.Authorization{
		ID: processorAuthorizationID, UserID: processorUserID, NodeID: processorNodeID, Enabled: true,
		ResetKind: "daily", Timezone: "UTC", ActivityWindowSeconds: 600,
		BlockDurationSeconds: 1800, SubscriptionTokenHash: make([]byte, 32),
	}, now.Add(-time.Hour)); err != nil {
		t.Fatalf("CreateAuthorization() error = %v", err)
	}
}
