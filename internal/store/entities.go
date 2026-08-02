package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (store *Store) deleteEntity(ctx context.Context, table, targetType, id, action string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s deletion: %w", targetType, err)
	}
	defer tx.Rollback()
	query := "DELETE FROM " + table + " WHERE id = ?" // table is selected by internal callers only.
	result, err := tx.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete %s: %w", targetType, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s deletion result: %w", targetType, err)
	}
	if deleted != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: action, TargetType: targetType, TargetID: id, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, targetType+" deletion")
}

func existsTx(ctx context.Context, tx *sql.Tx, table, id string) (bool, error) {
	query := "SELECT 1 FROM " + table + " WHERE id = ?" // table is selected by internal callers only.
	var exists int
	err := tx.QueryRowContext(ctx, query, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check %s record: %w", table, err)
	}
	return true, nil
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := fromUnix(value.Int64)
	return &result
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
