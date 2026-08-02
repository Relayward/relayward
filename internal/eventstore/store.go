package eventstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

var ErrGap = errors.New("event batch has a sequence gap")
var ErrConflict = errors.New("event conflicts with persisted data")

type StoredEvent struct {
	NodeID     string
	StreamID   string
	Event      agentv1.Event
	ReceivedAt time.Time
}

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("event database path is required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create event database directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect event database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("prepare event database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close prepared event database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("protect event database: %w", err)
	}
	dsn := url.URL{Scheme: "file", Path: path}
	query := dsn.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	dsn.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open event database: %w", err)
	}
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping event database: %w", err)
	}
	if err := migrate(ctx, database); err != nil {
		database.Close()
		return nil, err
	}
	return &Store{db: database}, nil
}

func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *Store) Ping(ctx context.Context) error {
	return store.db.PingContext(ctx)
}

func (store *Store) Ingest(ctx context.Context, nodeID string, batch agentv1.EventBatch, receivedAt time.Time) (uint64, error) {
	if err := agentv1.ValidateEventBatchForNode(nodeID, batch); err != nil {
		return 0, fmt.Errorf("validate event batch: %w", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin event ingestion: %w", err)
	}
	defer transaction.Rollback()
	highest, err := streamCursor(ctx, transaction, nodeID, batch.StreamID)
	if errors.Is(err, sql.ErrNoRows) {
		if batch.FirstSequence != 1 {
			return 0, ErrGap
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO event_streams(node_id, stream_id, highest_contiguous_sequence, updated_at)
VALUES (?, ?, 0, ?)`, nodeID, batch.StreamID, receivedAt.UTC().Unix()); err != nil {
			return 0, fmt.Errorf("create event stream: %w", err)
		}
		highest = 0
	} else if err != nil {
		return 0, fmt.Errorf("read event stream: %w", err)
	}
	if batch.FirstSequence > highest+1 {
		return 0, ErrGap
	}
	for _, event := range batch.Events {
		if err := ingestEvent(ctx, transaction, nodeID, batch.StreamID, event, receivedAt); err != nil {
			return 0, err
		}
	}
	if batch.LastSequence > highest {
		highest = batch.LastSequence
		if _, err := transaction.ExecContext(ctx, `
UPDATE event_streams SET highest_contiguous_sequence = ?, updated_at = ?
WHERE node_id = ? AND stream_id = ?`, highest, receivedAt.UTC().Unix(), nodeID, batch.StreamID); err != nil {
			return 0, fmt.Errorf("advance event stream: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit event ingestion: %w", err)
	}
	return batch.LastSequence, nil
}

func (store *Store) EventByID(ctx context.Context, eventID string) (StoredEvent, error) {
	var value StoredEvent
	var raw []byte
	var receivedAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT node_id, stream_id, event_json, received_at FROM events WHERE event_id = ?`, eventID).
		Scan(&value.NodeID, &value.StreamID, &raw, &receivedAt)
	if err != nil {
		return StoredEvent{}, err
	}
	if err := json.Unmarshal(raw, &value.Event); err != nil {
		return StoredEvent{}, fmt.Errorf("decode stored event: %w", err)
	}
	value.ReceivedAt = time.Unix(receivedAt, 0).UTC()
	return value, nil
}

func (store *Store) Count(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func streamCursor(ctx context.Context, transaction *sql.Tx, nodeID, streamID string) (uint64, error) {
	var highest int64
	err := transaction.QueryRowContext(ctx, `
SELECT highest_contiguous_sequence FROM event_streams WHERE node_id = ? AND stream_id = ?`, nodeID, streamID).Scan(&highest)
	return uint64(highest), err
}

func ingestEvent(ctx context.Context, transaction *sql.Tx, nodeID, streamID string, event agentv1.Event, receivedAt time.Time) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode event for storage: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
INSERT INTO events(event_id, node_id, stream_id, sequence, kind, observed_at, event_json, received_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, event.EventID, nodeID, streamID, int64(event.Sequence), event.Kind,
		event.ObservedAt.UTC().Format(time.RFC3339Nano), string(raw), receivedAt.UTC().Unix())
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read event insertion result: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var existingID string
	var existingRaw []byte
	err = transaction.QueryRowContext(ctx, `
SELECT event_id, event_json FROM events WHERE node_id = ? AND stream_id = ? AND sequence = ?`,
		nodeID, streamID, int64(event.Sequence)).Scan(&existingID, &existingRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("read duplicate event: %w", err)
	}
	if existingID != event.EventID || string(existingRaw) != string(raw) {
		return ErrConflict
	}
	return nil
}

func migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);`); err != nil {
		return fmt.Errorf("create event migration table: %w", err)
	}
	var applied int
	err := database.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = 1").Scan(&applied)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read event migration 1: %w", err)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin event migration 1: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
CREATE TABLE event_streams (
    node_id TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    highest_contiguous_sequence INTEGER NOT NULL DEFAULT 0 CHECK (highest_contiguous_sequence >= 0),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, stream_id)
);

CREATE TABLE IF NOT EXISTS events (
    event_id TEXT PRIMARY KEY CHECK (length(event_id) = 64),
    node_id TEXT NOT NULL,
    stream_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    kind TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    event_json TEXT NOT NULL CHECK (json_valid(event_json)),
    received_at INTEGER NOT NULL,
    UNIQUE (node_id, stream_id, sequence)
);
CREATE INDEX events_received_idx ON events(received_at, event_id);
CREATE INDEX events_node_kind_idx ON events(node_id, kind, received_at);
`); err != nil {
		return fmt.Errorf("apply event migration 1: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (1, unixepoch())"); err != nil {
		return fmt.Errorf("record event migration 1: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit event migration 1: %w", err)
	}
	return nil
}
