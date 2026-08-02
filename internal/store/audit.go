package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AuditEntry struct {
	ID         int64
	OccurredAt time.Time
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Outcome    string
	Metadata   map[string]any
}

func (store *Store) ListAudit(ctx context.Context, beforeID int64, limit int) ([]AuditEntry, error) {
	query := `
SELECT id, occurred_at, actor_type, actor_id, action, target_type, target_id, outcome, metadata_json
FROM audit_log`
	args := make([]any, 0, 2)
	if beforeID > 0 {
		query += " WHERE id < ?"
		args = append(args, beforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	entries := make([]AuditEntry, 0)
	for rows.Next() {
		var entry AuditEntry
		var occurredAt int64
		var metadata []byte
		if err := rows.Scan(&entry.ID, &occurredAt, &entry.ActorType, &entry.ActorID, &entry.Action,
			&entry.TargetType, &entry.TargetID, &entry.Outcome, &metadata); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		if err := json.Unmarshal(metadata, &entry.Metadata); err != nil {
			return nil, fmt.Errorf("decode audit metadata: %w", err)
		}
		entry.OccurredAt = fromUnix(occurredAt)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}

func (store *Store) AppendAudit(ctx context.Context, entry AuditEntry) error {
	return appendAudit(ctx, store.db, entry)
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func appendAuditTx(ctx context.Context, tx *sql.Tx, entry AuditEntry) error {
	return appendAudit(ctx, tx, entry)
}

func appendAudit(ctx context.Context, executor auditExecutor, entry AuditEntry) error {
	metadata := entry.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	_, err = executor.ExecContext(ctx, `
INSERT INTO audit_log(occurred_at, actor_type, actor_id, action, target_type, target_id, outcome, metadata_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, unixTime(entry.OccurredAt), entry.ActorType, entry.ActorID,
		entry.Action, entry.TargetType, entry.TargetID, entry.Outcome, string(encoded))
	if err != nil {
		return fmt.Errorf("append audit: %w", err)
	}
	return nil
}
