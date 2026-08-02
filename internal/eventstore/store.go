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
	RowID      int64
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
		if err := ingestEvent(ctx, transaction, nodeID, batch.StreamID, event, receivedAt, event.Sequence <= highest); err != nil {
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
	return highest, nil
}

func (store *Store) EventByID(ctx context.Context, eventID string) (StoredEvent, error) {
	var value StoredEvent
	var raw []byte
	var receivedAt int64
	err := store.db.QueryRowContext(ctx, `
	SELECT ingest_id, node_id, stream_id, event_json, received_at FROM events WHERE event_id = ?`, eventID).
		Scan(&value.RowID, &value.NodeID, &value.StreamID, &raw, &receivedAt)
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

func ingestEvent(ctx context.Context, transaction *sql.Tx, nodeID, streamID string, event agentv1.Event,
	receivedAt time.Time, allowPrunedReplay bool,
) error {
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
		if allowPrunedReplay {
			return nil
		}
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

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{version: 1, sql: `
	CREATE TABLE event_streams (
	    node_id TEXT NOT NULL,
	    stream_id TEXT NOT NULL,
	    highest_contiguous_sequence INTEGER NOT NULL DEFAULT 0 CHECK (highest_contiguous_sequence >= 0),
	    updated_at INTEGER NOT NULL,
	    PRIMARY KEY (node_id, stream_id)
	);

	CREATE TABLE events (
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
	`},
	{version: 2, sql: `
	CREATE TABLE consumer_cursors (
	    consumer_id TEXT PRIMARY KEY,
		    last_event_rowid INTEGER NOT NULL DEFAULT 0 CHECK (last_event_rowid >= 0),
		    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
		    failed_event_rowid INTEGER,
		    last_error TEXT NOT NULL DEFAULT '',
	    retry_after INTEGER,
	    updated_at INTEGER NOT NULL
	);

	CREATE TABLE normalized_access_events (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    node_id TEXT NOT NULL,
	    plugin_id TEXT NOT NULL,
	    source_stream_id TEXT NOT NULL,
	    source_event_id TEXT NOT NULL,
	    agent_event_id TEXT NOT NULL,
	    service_id TEXT NOT NULL,
	    authorization_id TEXT NOT NULL,
	    source_ip TEXT NOT NULL DEFAULT '',
	    destination TEXT NOT NULL DEFAULT '',
	    destination_port INTEGER NOT NULL DEFAULT 0 CHECK (destination_port BETWEEN 0 AND 65535),
	    network TEXT NOT NULL DEFAULT '',
	    protocol TEXT NOT NULL DEFAULT '',
	    action TEXT NOT NULL CHECK (action IN ('accepted', 'blocked')),
	    observed_at_ns INTEGER NOT NULL,
	    observed_day TEXT NOT NULL CHECK (length(observed_day) = 10),
	    received_at INTEGER NOT NULL,
	    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
	    UNIQUE (node_id, plugin_id, source_stream_id, source_event_id)
	);
	CREATE INDEX normalized_access_recent_idx ON normalized_access_events(observed_at_ns DESC, id DESC);
	CREATE INDEX normalized_access_node_recent_idx ON normalized_access_events(node_id, observed_at_ns DESC, id DESC);
	CREATE INDEX normalized_access_archive_idx ON normalized_access_events(observed_day, id);

	CREATE TABLE access_archive_days (
	    day TEXT PRIMARY KEY CHECK (length(day) = 10),
	    relative_path TEXT NOT NULL,
	    event_count INTEGER NOT NULL CHECK (event_count > 0),
	    max_access_id INTEGER NOT NULL CHECK (max_access_id > 0),
	    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
	    completed_at INTEGER NOT NULL,
	    updated_at INTEGER NOT NULL
	);
	`},
	{version: 3, sql: `
	CREATE TABLE events_v3 (
	    ingest_id INTEGER PRIMARY KEY AUTOINCREMENT,
	    event_id TEXT NOT NULL UNIQUE CHECK (length(event_id) = 64),
	    node_id TEXT NOT NULL,
	    stream_id TEXT NOT NULL,
	    sequence INTEGER NOT NULL CHECK (sequence > 0),
	    kind TEXT NOT NULL,
	    observed_at TEXT NOT NULL,
	    event_json TEXT NOT NULL CHECK (json_valid(event_json)),
	    received_at INTEGER NOT NULL,
	    UNIQUE (node_id, stream_id, sequence)
	);

	INSERT INTO events_v3(
	    ingest_id, event_id, node_id, stream_id, sequence, kind, observed_at, event_json, received_at
	)
	SELECT rowid, event_id, node_id, stream_id, sequence, kind, observed_at, event_json, received_at
	FROM events ORDER BY rowid;

	DELETE FROM sqlite_sequence WHERE name = 'events_v3';
	INSERT INTO sqlite_sequence(name, seq)
	SELECT 'events_v3', MAX(position) FROM (
	    SELECT COALESCE(MAX(ingest_id), 0) AS position FROM events_v3
	    UNION ALL
	    SELECT COALESCE(MAX(last_event_rowid), 0) FROM consumer_cursors
	);

	DROP TABLE events;
	ALTER TABLE events_v3 RENAME TO events;
	CREATE INDEX events_received_idx ON events(received_at, event_id);
	CREATE INDEX events_node_kind_idx ON events(node_id, kind, received_at);
	`},
}

func migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);`); err != nil {
		return fmt.Errorf("create event migration table: %w", err)
	}
	for _, item := range migrations {
		var applied int
		err := database.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", item.version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read event migration %d: %w", item.version, err)
		}
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin event migration %d: %w", item.version, err)
		}
		if _, err := transaction.ExecContext(ctx, item.sql); err != nil {
			transaction.Rollback()
			return fmt.Errorf("apply event migration %d: %w", item.version, err)
		}
		if _, err := transaction.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, unixepoch())", item.version); err != nil {
			transaction.Rollback()
			return fmt.Errorf("record event migration %d: %w", item.version, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit event migration %d: %w", item.version, err)
		}
	}
	return nil
}
