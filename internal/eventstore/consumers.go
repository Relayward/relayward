package eventstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

const maximumStoredConsumerErrorBytes = 1024

type ConsumerState struct {
	ID                  string
	LastEventRowID      int64
	ConsecutiveFailures int
	FailedEventRowID    *int64
	LastError           string
	RetryAfter          *time.Time
	UpdatedAt           time.Time
}

type AccessRecord struct {
	ID              int64     `json:"id"`
	NodeID          string    `json:"node_id"`
	PluginID        string    `json:"plugin_id"`
	SourceStreamID  string    `json:"source_stream_id"`
	SourceEventID   string    `json:"source_event_id"`
	AgentEventID    string    `json:"agent_event_id"`
	ServiceID       string    `json:"service_id"`
	AuthorizationID string    `json:"authorization_id"`
	SourceIP        string    `json:"source_ip,omitempty"`
	Destination     string    `json:"destination,omitempty"`
	DestinationPort uint32    `json:"destination_port,omitempty"`
	Network         string    `json:"network,omitempty"`
	Protocol        string    `json:"protocol,omitempty"`
	Action          string    `json:"action"`
	ObservedAt      time.Time `json:"observed_at"`
	ReceivedAt      time.Time `json:"received_at"`
	PayloadSHA256   string    `json:"payload_sha256"`
}

type ArchiveDay struct {
	Day          string
	RelativePath string
	EventCount   int64
	MaxAccessID  int64
	SHA256       string
	CompletedAt  time.Time
	UpdatedAt    time.Time
}

type ArchiveCandidate struct {
	Day                string
	EventCount         int64
	MaxAccessID        int64
	ArchivedEventCount int64
	ArchivedMaxID      int64
	ArchivedPath       string
	ArchivedSHA256     string
}

func (store *Store) EnsureConsumer(ctx context.Context, consumerID string, now time.Time) error {
	if consumerID == "" || now.IsZero() {
		return errors.New("consumer ID and initialization time are required")
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO consumer_cursors(consumer_id, updated_at) VALUES (?, ?)
ON CONFLICT(consumer_id) DO NOTHING`, consumerID, now.UTC().Unix())
	if err != nil {
		return fmt.Errorf("initialize consumer cursor: %w", err)
	}
	return nil
}

func (store *Store) ReadConsumerBatch(ctx context.Context, consumerID string, limit int) ([]StoredEvent, error) {
	if consumerID == "" {
		return nil, errors.New("consumer ID is required")
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("consumer batch limit must be between 1 and 1000")
	}
	var cursor int64
	err := store.db.QueryRowContext(ctx, `
SELECT last_event_rowid FROM consumer_cursors WHERE consumer_id = ?`, consumerID).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read consumer cursor: %w", err)
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT ingest_id, node_id, stream_id, event_json, received_at
FROM events WHERE ingest_id > ? ORDER BY ingest_id LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("read consumer event batch: %w", err)
	}
	defer rows.Close()
	values := make([]StoredEvent, 0)
	for rows.Next() {
		var value StoredEvent
		var raw []byte
		var receivedAt int64
		if err := rows.Scan(&value.RowID, &value.NodeID, &value.StreamID, &raw, &receivedAt); err != nil {
			return nil, fmt.Errorf("scan consumer event: %w", err)
		}
		if err := json.Unmarshal(raw, &value.Event); err != nil {
			return nil, fmt.Errorf("decode consumer event: %w", err)
		}
		value.ReceivedAt = time.Unix(receivedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate consumer events: %w", err)
	}
	return values, nil
}

func (store *Store) AdvanceConsumer(ctx context.Context, consumerID string, eventRowID int64, now time.Time) error {
	if consumerID == "" || eventRowID < 1 || now.IsZero() {
		return errors.New("invalid consumer advancement")
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO consumer_cursors(
    consumer_id, last_event_rowid, consecutive_failures, failed_event_rowid, last_error, retry_after, updated_at
) VALUES (?, ?, 0, NULL, '', NULL, ?)
ON CONFLICT(consumer_id) DO UPDATE SET
    last_event_rowid = excluded.last_event_rowid,
    consecutive_failures = 0,
    failed_event_rowid = NULL,
    last_error = '',
    retry_after = NULL,
    updated_at = excluded.updated_at
WHERE excluded.last_event_rowid >= consumer_cursors.last_event_rowid`, consumerID, eventRowID, now.UTC().Unix())
	if err != nil {
		return fmt.Errorf("advance consumer cursor: %w", err)
	}
	return nil
}

func (store *Store) RecordConsumerFailure(ctx context.Context, consumerID string, eventRowID int64, failure error,
	retryAt, now time.Time,
) error {
	if consumerID == "" || eventRowID < 1 || failure == nil || retryAt.IsZero() || now.IsZero() {
		return errors.New("invalid consumer failure")
	}
	message := failure.Error()
	if len(message) > maximumStoredConsumerErrorBytes {
		message = message[:maximumStoredConsumerErrorBytes]
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO consumer_cursors(
    consumer_id, last_event_rowid, consecutive_failures, failed_event_rowid, last_error, retry_after, updated_at
) VALUES (?, 0, 1, ?, ?, ?, ?)
ON CONFLICT(consumer_id) DO UPDATE SET
    consecutive_failures = consumer_cursors.consecutive_failures + 1,
    failed_event_rowid = excluded.failed_event_rowid,
    last_error = excluded.last_error,
    retry_after = excluded.retry_after,
    updated_at = excluded.updated_at`, consumerID, eventRowID, message, retryAt.UTC().Unix(), now.UTC().Unix())
	if err != nil {
		return fmt.Errorf("record consumer failure at event %d: %w", eventRowID, err)
	}
	return nil
}

func (store *Store) ConsumerState(ctx context.Context, consumerID string) (ConsumerState, error) {
	var value ConsumerState
	var failedEventRowID, retryAfter sql.NullInt64
	var updatedAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT consumer_id, last_event_rowid, consecutive_failures, failed_event_rowid, last_error, retry_after, updated_at
FROM consumer_cursors WHERE consumer_id = ?`, consumerID).Scan(
		&value.ID, &value.LastEventRowID, &value.ConsecutiveFailures, &failedEventRowID,
		&value.LastError, &retryAfter, &updatedAt)
	if err != nil {
		return ConsumerState{}, err
	}
	if retryAfter.Valid {
		parsed := time.Unix(retryAfter.Int64, 0).UTC()
		value.RetryAfter = &parsed
	}
	if failedEventRowID.Valid {
		value.FailedEventRowID = &failedEventRowID.Int64
	}
	value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return value, nil
}

func (store *Store) StoreAccessEvent(ctx context.Context, source StoredEvent, value agentv1.AccessEvent) error {
	if source.RowID < 1 {
		return errors.New("source event row ID is required")
	}
	if err := agentv1.ValidateAccessEvent(value); err != nil {
		return fmt.Errorf("validate access event: %w", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode access event digest: %w", err)
	}
	digest := sha256.Sum256(raw)
	digestText := hex.EncodeToString(digest[:])
	result, err := store.db.ExecContext(ctx, `
INSERT INTO normalized_access_events(
    node_id, plugin_id, source_stream_id, source_event_id, agent_event_id,
    service_id, authorization_id, source_ip, destination, destination_port,
    network, protocol, action, observed_at_ns, observed_day, received_at, payload_sha256
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id, plugin_id, source_stream_id, source_event_id) DO NOTHING`,
		source.NodeID, value.PluginID, value.SourceStreamID, value.SourceEventID, source.Event.EventID,
		value.ServiceID, value.AuthorizationID, value.SourceIP, value.Destination, int64(value.DestinationPort),
		value.Network, value.Protocol, value.Action, source.Event.ObservedAt.UTC().UnixNano(),
		source.Event.ObservedAt.UTC().Format(time.DateOnly), source.ReceivedAt.UTC().Unix(), digestText)
	if err != nil {
		return fmt.Errorf("store normalized access event: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read normalized access event insertion: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var existingDigest string
	err = store.db.QueryRowContext(ctx, `
SELECT payload_sha256 FROM normalized_access_events
WHERE node_id = ? AND plugin_id = ? AND source_stream_id = ? AND source_event_id = ?`,
		source.NodeID, value.PluginID, value.SourceStreamID, value.SourceEventID).Scan(&existingDigest)
	if err != nil {
		return fmt.Errorf("read duplicate normalized access event: %w", err)
	}
	if existingDigest != digestText {
		return ErrConflict
	}
	return nil
}

func (store *Store) RecentAccessEvents(ctx context.Context, nodeID string, beforeID int64, limit int) ([]AccessRecord, error) {
	if beforeID < 0 || limit < 1 || limit > 500 {
		return nil, errors.New("invalid recent access event query")
	}
	query := accessRecordSelect
	args := make([]any, 0, 3)
	clauses := make([]string, 0, 2)
	if nodeID != "" {
		clauses = append(clauses, "node_id = ?")
		args = append(args, nodeID)
	}
	if beforeID > 0 {
		clauses = append(clauses, "id < ?")
		args = append(args, beforeID)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list recent access events: %w", err)
	}
	defer rows.Close()
	values := make([]AccessRecord, 0)
	for rows.Next() {
		value, err := scanAccessRecord(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent access events: %w", err)
	}
	return values, nil
}

func (store *Store) PendingAccessArchiveDays(ctx context.Context, beforeDay string) ([]ArchiveCandidate, error) {
	if err := validateDay(beforeDay); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT e.observed_day,
       coalesce(a.event_count, 0) + sum(CASE WHEN e.id > coalesce(a.max_access_id, 0) THEN 1 ELSE 0 END),
       max(e.id), coalesce(a.event_count, 0), coalesce(a.max_access_id, 0),
       coalesce(a.relative_path, ''), coalesce(a.sha256, '')
FROM normalized_access_events e
LEFT JOIN access_archive_days a ON a.day = e.observed_day
WHERE e.observed_day < ?
GROUP BY e.observed_day, a.event_count, a.max_access_id, a.relative_path, a.sha256
HAVING a.day IS NULL OR max(e.id) > a.max_access_id
ORDER BY e.observed_day`, beforeDay)
	if err != nil {
		return nil, fmt.Errorf("list pending access archive days: %w", err)
	}
	defer rows.Close()
	values := make([]ArchiveCandidate, 0)
	for rows.Next() {
		var value ArchiveCandidate
		if err := rows.Scan(&value.Day, &value.EventCount, &value.MaxAccessID,
			&value.ArchivedEventCount, &value.ArchivedMaxID, &value.ArchivedPath, &value.ArchivedSHA256); err != nil {
			return nil, fmt.Errorf("scan pending access archive day: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending access archive days: %w", err)
	}
	return values, nil
}

func (store *Store) AccessEventsForDay(ctx context.Context, day string) ([]AccessRecord, error) {
	values := make([]AccessRecord, 0)
	_, _, err := store.VisitAccessEventsForDay(ctx, day, func(value AccessRecord) error {
		values = append(values, value)
		return nil
	})
	return values, err
}

func (store *Store) VisitAccessEventsForDay(ctx context.Context, day string, visit func(AccessRecord) error) (int64, int64, error) {
	return store.VisitAccessEventsForDayAfterID(ctx, day, 0, visit)
}

func (store *Store) VisitAccessEventsForDayAfterID(ctx context.Context, day string, afterID int64,
	visit func(AccessRecord) error,
) (int64, int64, error) {
	if err := validateDay(day); err != nil {
		return 0, 0, err
	}
	if afterID < 0 {
		return 0, 0, errors.New("access archive cursor must not be negative")
	}
	if visit == nil {
		return 0, 0, errors.New("access archive visitor is required")
	}
	rows, err := store.db.QueryContext(ctx, accessRecordSelect+" WHERE observed_day = ? AND id > ? ORDER BY id", day, afterID)
	if err != nil {
		return 0, 0, fmt.Errorf("read access archive day: %w", err)
	}
	defer rows.Close()
	var count, maximumID int64
	for rows.Next() {
		value, err := scanAccessRecord(rows)
		if err != nil {
			return 0, 0, err
		}
		if err := visit(value); err != nil {
			return 0, 0, err
		}
		count++
		maximumID = value.ID
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterate access archive day: %w", err)
	}
	return count, maximumID, nil
}

func (store *Store) RecordAccessArchive(ctx context.Context, value ArchiveDay) error {
	if err := validateDay(value.Day); err != nil {
		return err
	}
	if value.RelativePath == "" || value.EventCount < 1 || value.MaxAccessID < 1 || len(value.SHA256) != 64 || value.CompletedAt.IsZero() {
		return errors.New("invalid access archive metadata")
	}
	_, err := store.db.ExecContext(ctx, `
INSERT INTO access_archive_days(day, relative_path, event_count, max_access_id, sha256, completed_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(day) DO UPDATE SET relative_path = excluded.relative_path, event_count = excluded.event_count,
    max_access_id = excluded.max_access_id, sha256 = excluded.sha256,
    completed_at = excluded.completed_at, updated_at = excluded.updated_at`,
		value.Day, value.RelativePath, value.EventCount, value.MaxAccessID, value.SHA256,
		value.CompletedAt.UTC().Unix(), value.CompletedAt.UTC().Unix())
	if err != nil {
		return fmt.Errorf("record access archive: %w", err)
	}
	return nil
}

func (store *Store) AccessArchivesBefore(ctx context.Context, beforeDay string) ([]ArchiveDay, error) {
	if err := validateDay(beforeDay); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT day, relative_path, event_count, max_access_id, sha256, completed_at, updated_at
FROM access_archive_days WHERE day < ? ORDER BY day`, beforeDay)
	if err != nil {
		return nil, fmt.Errorf("list expired access archives: %w", err)
	}
	defer rows.Close()
	values := make([]ArchiveDay, 0)
	for rows.Next() {
		var value ArchiveDay
		var completedAt, updatedAt int64
		if err := rows.Scan(&value.Day, &value.RelativePath, &value.EventCount, &value.MaxAccessID,
			&value.SHA256, &completedAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan access archive: %w", err)
		}
		value.CompletedAt = time.Unix(completedAt, 0).UTC()
		value.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate access archives: %w", err)
	}
	return values, nil
}

func (store *Store) DeleteAccessArchive(ctx context.Context, day string) error {
	if err := validateDay(day); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, "DELETE FROM access_archive_days WHERE day = ?", day)
	return err
}

func (store *Store) PruneHotData(ctx context.Context, cutoff time.Time, consumerIDs []string) (int64, int64, error) {
	if cutoff.IsZero() || len(consumerIDs) == 0 {
		return 0, 0, errors.New("hot data cutoff and consumers are required")
	}
	minimumCursor := int64(^uint64(0) >> 1)
	for _, consumerID := range consumerIDs {
		state, err := store.ConsumerState(ctx, consumerID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return 0, 0, nil
			}
			return 0, 0, err
		}
		if state.LastEventRowID < minimumCursor {
			minimumCursor = state.LastEventRowID
		}
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin hot event pruning: %w", err)
	}
	defer tx.Rollback()
	accessResult, err := tx.ExecContext(ctx, `
DELETE FROM normalized_access_events
WHERE observed_at_ns < ? AND EXISTS (
    SELECT 1 FROM access_archive_days a
    WHERE a.day = normalized_access_events.observed_day
      AND a.max_access_id >= normalized_access_events.id
)`, cutoff.UTC().UnixNano())
	if err != nil {
		return 0, 0, fmt.Errorf("prune normalized access events: %w", err)
	}
	rawResult, err := tx.ExecContext(ctx, `
	DELETE FROM events WHERE ingest_id <= ? AND received_at < ?`, minimumCursor, cutoff.UTC().Unix())
	if err != nil {
		return 0, 0, fmt.Errorf("prune raw events: %w", err)
	}
	accessCount, _ := accessResult.RowsAffected()
	rawCount, _ := rawResult.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit hot event pruning: %w", err)
	}
	return rawCount, accessCount, nil
}

const accessRecordSelect = `
SELECT id, node_id, plugin_id, source_stream_id, source_event_id, agent_event_id,
       service_id, authorization_id, source_ip, destination, destination_port,
       network, protocol, action, observed_at_ns, received_at, payload_sha256
FROM normalized_access_events`

func scanAccessRecord(row interface{ Scan(...any) error }) (AccessRecord, error) {
	var value AccessRecord
	var destinationPort uint32
	var observedAtNS, receivedAt int64
	if err := row.Scan(&value.ID, &value.NodeID, &value.PluginID, &value.SourceStreamID,
		&value.SourceEventID, &value.AgentEventID, &value.ServiceID, &value.AuthorizationID,
		&value.SourceIP, &value.Destination, &destinationPort, &value.Network, &value.Protocol,
		&value.Action, &observedAtNS, &receivedAt, &value.PayloadSHA256); err != nil {
		return AccessRecord{}, fmt.Errorf("scan normalized access event: %w", err)
	}
	value.DestinationPort = destinationPort
	value.ObservedAt = time.Unix(0, observedAtNS).UTC()
	value.ReceivedAt = time.Unix(receivedAt, 0).UTC()
	return value, nil
}

func validateDay(value string) error {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil || parsed.Format(time.DateOnly) != value {
		return errors.New("archive day must use YYYY-MM-DD")
	}
	return nil
}
