package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AuditEntry struct {
	OccurredAt time.Time
	ActorType  string
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	Outcome    string
	Metadata   map[string]any
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
