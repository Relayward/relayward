package eventstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

const testNodeID = "123e4567-e89b-42d3-a456-426614174000"
const testStreamID = "0123456789abcdef0123456789abcdef"

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
