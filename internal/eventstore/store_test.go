package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
)

const testNodeID = "123e4567-e89b-42d3-a456-426614174000"
const testStreamID = "0123456789abcdef0123456789abcdef"

func TestOpenCreatesCurrentBaselineSchema(t *testing.T) {
	ctx := context.Background()
	if len(migrations) != 1 || migrations[0].version != 1 {
		t.Fatalf("migrations = %+v, want one version 1 baseline", migrations)
	}
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	var count, minimum, maximum int
	if err := store.db.QueryRowContext(ctx, `
SELECT count(*), min(version), max(version) FROM schema_migrations`).Scan(&count, &minimum, &maximum); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count != 1 || minimum != 1 || maximum != 1 {
		t.Fatalf("migration summary = count %d, min %d, max %d; want only version 1", count, minimum, maximum)
	}
	assertSchemaObjects(t, store.db, "table", []string{
		"event_streams", "events", "consumer_cursors", "normalized_access_events", "access_archive_days",
	})
	assertSchemaObjects(t, store.db, "index", []string{
		"events_received_idx", "events_node_kind_idx", "normalized_access_recent_idx",
		"normalized_access_node_recent_idx", "normalized_access_archive_idx",
	})
	assertTableColumns(t, store.db, "events", []string{
		"ingest_id", "event_id", "node_id", "stream_id", "sequence", "kind",
		"observed_at", "event_json", "received_at",
	})
	assertTableColumns(t, store.db, "consumer_cursors", []string{
		"last_event_rowid", "consecutive_failures", "failed_event_rowid", "last_error", "retry_after",
	})
	assertTableColumns(t, store.db, "normalized_access_events", []string{
		"source_stream_id", "source_event_id", "agent_event_id", "authorization_id",
		"observed_at_ns", "observed_day", "payload_sha256",
	})
}

func TestIngestPersistsAndIdempotentlyReplaysContiguousEvents(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	batch := testBatch(t, 1, 2, now)
	highest, err := store.Ingest(ctx, testNodeID, batch, now)
	if err != nil || highest != 2 {
		t.Fatalf("Ingest() highest = %d, error = %v", highest, err)
	}
	if highest, err := store.Ingest(ctx, testNodeID, batch, now.Add(time.Second)); err != nil || highest != 2 {
		t.Fatalf("replayed Ingest() highest = %d, error = %v", highest, err)
	}
	count, err := store.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d, %v", count, err)
	}
	stored, err := store.EventByID(ctx, batch.Events[0].EventID)
	if err != nil || stored.NodeID != testNodeID || stored.Event.Sequence != 1 || !stored.ReceivedAt.Equal(now.Truncate(time.Second)) {
		t.Fatalf("EventByID() = %+v, %v", stored, err)
	}
}

func TestPublishPluginEventsIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 2, 16, 0, 0, 0, time.UTC)
	pluginID := "io.relayward.risk"
	request := &centerpluginv1.PublishEventsRequest{Events: []*centerpluginv1.PublishedEvent{
		{SourceEventId: "event-1", NodeId: testNodeID, Kind: centerpluginv1.EventNotificationRequest, ObservedAtUnixNano: now.UnixNano(),
			Json: []byte(`{"severity":"warning","subject":"Risk window","body":"Review required.","dedup_key":"risk:1"}`)},
		{SourceEventId: "event-2", NodeId: testNodeID, Kind: "plugin.io.relayward.risk.window", ObservedAtUnixNano: now.Add(time.Second).UnixNano(),
			Json: []byte(`{"risk_count":1}`)},
	}}
	if err := store.PublishPluginEvents(ctx, pluginID, request, now.Add(2*time.Second)); err != nil {
		t.Fatalf("PublishPluginEvents() error = %v", err)
	}
	if err := store.PublishPluginEvents(ctx, pluginID, request, now.Add(3*time.Second)); err != nil {
		t.Fatalf("replayed PublishPluginEvents() error = %v", err)
	}
	count, err := store.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d, %v", count, err)
	}
	stored, err := store.EventByID(ctx, pluginEventID(pluginID, "event-1"))
	if err != nil || stored.NodeID != testNodeID || stored.Event.Sequence != 1 || stored.Event.Kind != centerpluginv1.EventNotificationRequest {
		t.Fatalf("EventByID() = %+v, %v", stored, err)
	}

	request.Events[0].Json = []byte(`{"severity":"warning","subject":"Risk window","body":"Changed."}`)
	if err := store.PublishPluginEvents(ctx, pluginID, request, now.Add(4*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting PublishPluginEvents() error = %v", err)
	}
	count, err = store.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("Count() after conflict = %d, %v", count, err)
	}
}

func TestIngestRejectsGapAndConflictingReplay(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if _, err := store.Ingest(ctx, testNodeID, testBatch(t, 2, 2, now), now); !errors.Is(err, ErrGap) {
		t.Fatalf("Ingest() initial gap error = %v", err)
	}
	batch := testBatch(t, 1, 1, now)
	if _, err := store.Ingest(ctx, testNodeID, batch, now); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	conflicting, err := agentv1.NewEvent(testNodeID, testStreamID, 1, "system.test", now, map[string]bool{"ok": false})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	batch.Events[0] = conflicting
	if _, err := store.Ingest(ctx, testNodeID, batch, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("Ingest() conflicting replay error = %v", err)
	}
	count, _ := store.Count(ctx)
	if count != 1 {
		t.Fatalf("Count() after conflict = %d", count)
	}
}

func TestIngestAcceptsOverlapAfterLostAcknowledgement(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if _, err := store.Ingest(ctx, testNodeID, testBatch(t, 1, 2, now), now); err != nil {
		t.Fatalf("first Ingest() error = %v", err)
	}
	overlap := testBatch(t, 2, 3, now)
	if highest, err := store.Ingest(ctx, testNodeID, overlap, now); err != nil || highest != 3 {
		t.Fatalf("overlap Ingest() highest = %d, error = %v", highest, err)
	}
	count, _ := store.Count(ctx)
	if count != 3 {
		t.Fatalf("Count() after overlap = %d", count)
	}
}

func TestIngestPositionRemainsMonotonicAfterHotDataIsFullyPruned(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	first := testBatch(t, 1, 1, now)
	if _, err := store.Ingest(ctx, testNodeID, first, now); err != nil {
		t.Fatalf("first Ingest() error = %v", err)
	}
	stored, err := store.EventByID(ctx, first.Events[0].EventID)
	if err != nil {
		t.Fatalf("first EventByID() error = %v", err)
	}
	if err := store.EnsureConsumer(ctx, "test.consumer", now); err != nil {
		t.Fatalf("EnsureConsumer() error = %v", err)
	}
	if err := store.AdvanceConsumer(ctx, "test.consumer", stored.RowID, now); err != nil {
		t.Fatalf("AdvanceConsumer() error = %v", err)
	}
	deleted, _, err := store.PruneHotData(ctx, now.Add(time.Hour), []string{"test.consumer"})
	if err != nil || deleted != 1 {
		t.Fatalf("PruneHotData() deleted = %d, error = %v", deleted, err)
	}

	second := testBatch(t, 2, 2, now.Add(2*time.Hour))
	if _, err := store.Ingest(ctx, testNodeID, second, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("second Ingest() error = %v", err)
	}
	batch, err := store.ReadConsumerBatch(ctx, "test.consumer", 10)
	if err != nil || len(batch) != 1 || batch[0].Event.Sequence != 2 || batch[0].RowID <= stored.RowID {
		t.Fatalf("consumer batch after prune = %+v, error = %v", batch, err)
	}
}

func TestStoreAccessEventDeduplicatesPluginSourceIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	access := agentv1.AccessEvent{
		SourceStreamID: testStreamID, SourceEventID: "runtime-event-1", PluginID: "runtime.test",
		ServiceID: "main", AuthorizationID: "30000000-0000-4000-8000-000000000003",
		SourceIP: "192.0.2.10", Destination: "example.com", DestinationPort: 443,
		Network: "tcp", Protocol: "tls", Action: agentv1.AccessActionAccepted,
	}
	event, err := agentv1.NewEvent(testNodeID, testStreamID, 1, agentv1.EventAccess, now, access)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	source := StoredEvent{RowID: 1, NodeID: testNodeID, StreamID: testStreamID, Event: event, ReceivedAt: now}
	if err := store.StoreAccessEvent(ctx, source, access); err != nil {
		t.Fatalf("StoreAccessEvent() error = %v", err)
	}
	if err := store.StoreAccessEvent(ctx, source, access); err != nil {
		t.Fatalf("StoreAccessEvent() idempotent replay error = %v", err)
	}
	conflict := access
	conflict.Destination = "changed.example"
	if err := store.StoreAccessEvent(ctx, source, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("StoreAccessEvent() conflicting source identity error = %v", err)
	}
	values, err := store.RecentAccessEvents(ctx, testNodeID, 0, 10)
	if err != nil || len(values) != 1 || values[0].Destination != access.Destination {
		t.Fatalf("RecentAccessEvents() = %+v, %v", values, err)
	}
}

func testBatch(t *testing.T, first, last uint64, observedAt time.Time) agentv1.EventBatch {
	t.Helper()
	events := make([]agentv1.Event, 0, last-first+1)
	for sequence := first; sequence <= last; sequence++ {
		event, err := agentv1.NewEvent(testNodeID, testStreamID, sequence, "system.test", observedAt.Add(time.Duration(sequence)*time.Millisecond), map[string]uint64{"sequence": sequence})
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		events = append(events, event)
	}
	return agentv1.EventBatch{
		APIVersion: agentv1.APIVersion, NodeID: testNodeID, StreamID: testStreamID,
		FirstSequence: first, LastSequence: last, Events: events,
	}
}

func assertSchemaObjects(t *testing.T, database *sql.DB, objectType string, names []string) {
	t.Helper()
	for _, name := range names {
		var count int
		if err := database.QueryRow(`
SELECT count(*) FROM sqlite_schema WHERE type = ? AND name = ?`, objectType, name).Scan(&count); err != nil {
			t.Fatalf("query %s %q: %v", objectType, name, err)
		}
		if count != 1 {
			t.Errorf("%s %q count = %d, want 1", objectType, name, count)
		}
	}
}

func assertTableColumns(t *testing.T, database *sql.DB, table string, expected []string) {
	t.Helper()
	rows, err := database.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		t.Fatalf("query columns for %q: %v", table, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column for %q: %v", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %q: %v", table, err)
	}
	for _, name := range expected {
		if _, ok := columns[name]; !ok {
			t.Errorf("table %q is missing column %q", table, name)
		}
	}
}
