package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Authorization struct {
	ID                    string
	UserID                string
	NodeID                string
	Enabled               bool
	TrafficLimitBytes     *int64
	ResetKind             string
	ResetValue            *int
	Timezone              string
	PeriodAnchor          *time.Time
	ExpiresAt             *time.Time
	SoftIPLimit           *int
	ActivityWindowSeconds int
	BlockDurationSeconds  int
	SubscriptionTokenHash []byte
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (store *Store) ListAuthorizations(ctx context.Context, userID, nodeID string) ([]Authorization, error) {
	query := `
SELECT id, user_id, node_id, enabled, traffic_limit_bytes, reset_kind, reset_value, timezone,
       period_anchor, expires_at, soft_ip_limit, activity_window_seconds, block_duration_seconds,
       subscription_token_hash, created_at, updated_at
FROM authorizations`
	args := make([]any, 0, 2)
	switch {
	case userID != "" && nodeID != "":
		query += " WHERE user_id = ? AND node_id = ?"
		args = append(args, userID, nodeID)
	case userID != "":
		query += " WHERE user_id = ?"
		args = append(args, userID)
	case nodeID != "":
		query += " WHERE node_id = ?"
		args = append(args, nodeID)
	}
	query += " ORDER BY created_at DESC, id"
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list authorizations: %w", err)
	}
	defer rows.Close()

	values := make([]Authorization, 0)
	for rows.Next() {
		value, err := scanAuthorization(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authorizations: %w", err)
	}
	return values, nil
}

func (store *Store) AuthorizationByID(ctx context.Context, id string) (Authorization, error) {
	return scanAuthorization(store.db.QueryRowContext(ctx, `
SELECT id, user_id, node_id, enabled, traffic_limit_bytes, reset_kind, reset_value, timezone,
       period_anchor, expires_at, soft_ip_limit, activity_window_seconds, block_duration_seconds,
       subscription_token_hash, created_at, updated_at
FROM authorizations WHERE id = ?`, id))
}

func (store *Store) CreateAuthorization(ctx context.Context, value Authorization, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin authorization creation: %w", err)
	}
	defer tx.Rollback()
	for _, reference := range []struct{ table, id string }{{"users", value.UserID}, {"nodes", value.NodeID}} {
		exists, err := existsTx(ctx, tx, reference.table, reference.id)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO authorizations(
    id, user_id, node_id, enabled, traffic_limit_bytes, reset_kind, reset_value, timezone,
    period_anchor, expires_at, soft_ip_limit, activity_window_seconds, block_duration_seconds,
    subscription_token_hash, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, value.ID, value.UserID, value.NodeID, boolInt(value.Enabled), value.TrafficLimitBytes,
		value.ResetKind, value.ResetValue, value.Timezone, nullableUnix(value.PeriodAnchor), nullableUnix(value.ExpiresAt),
		value.SoftIPLimit, value.ActivityWindowSeconds, value.BlockDurationSeconds, value.SubscriptionTokenHash,
		unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert authorization: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read authorization creation result: %w", err)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "authorization.create",
		TargetType: "authorization", TargetID: value.ID, Outcome: "success",
		Metadata: map[string]any{"user_id": value.UserID, "node_id": value.NodeID},
	}); err != nil {
		return err
	}
	return commit(tx, "authorization creation")
}

func (store *Store) UpdateAuthorization(ctx context.Context, value Authorization, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin authorization update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE authorizations SET
    enabled = ?, traffic_limit_bytes = ?, reset_kind = ?, reset_value = ?, timezone = ?,
    period_anchor = ?, expires_at = ?, soft_ip_limit = ?, activity_window_seconds = ?,
    block_duration_seconds = ?, updated_at = ?
WHERE id = ?`, boolInt(value.Enabled), value.TrafficLimitBytes, value.ResetKind, value.ResetValue,
		value.Timezone, nullableUnix(value.PeriodAnchor), nullableUnix(value.ExpiresAt), value.SoftIPLimit,
		value.ActivityWindowSeconds, value.BlockDurationSeconds, unixTime(now), value.ID)
	if err != nil {
		return fmt.Errorf("update authorization: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read authorization update result: %w", err)
	}
	if updated != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "authorization.update", TargetType: "authorization", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "authorization update")
}

func (store *Store) DeleteAuthorization(ctx context.Context, id string, now time.Time) error {
	return store.deleteEntity(ctx, "authorizations", "authorization", id, "authorization.delete", now)
}

func (store *Store) RotateSubscriptionToken(ctx context.Context, id string, tokenHash []byte, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin subscription token rotation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE OR IGNORE authorizations SET subscription_token_hash = ?, updated_at = ? WHERE id = ?`, tokenHash, unixTime(now), id)
	if err != nil {
		return fmt.Errorf("rotate subscription token: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read subscription token rotation result: %w", err)
	}
	if updated != 1 {
		exists, err := existsTx(ctx, tx, "authorizations", id)
		if err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "authorization.subscription_token.rotate", TargetType: "authorization", TargetID: id, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "subscription token rotation")
}

func scanAuthorization(row rowScanner) (Authorization, error) {
	var value Authorization
	var enabled int
	var trafficLimit, resetValue, periodAnchor, expiresAt, softIPLimit sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.UserID, &value.NodeID, &enabled, &trafficLimit, &value.ResetKind,
		&resetValue, &value.Timezone, &periodAnchor, &expiresAt, &softIPLimit, &value.ActivityWindowSeconds,
		&value.BlockDurationSeconds, &value.SubscriptionTokenHash, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Authorization{}, ErrNotFound
		}
		return Authorization{}, fmt.Errorf("scan authorization: %w", err)
	}
	value.Enabled = enabled != 0
	value.TrafficLimitBytes = nullableInt64(trafficLimit)
	value.ResetValue = nullableInt(resetValue)
	value.PeriodAnchor = nullableTime(periodAnchor)
	value.ExpiresAt = nullableTime(expiresAt)
	value.SoftIPLimit = nullableInt(softIPLimit)
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
