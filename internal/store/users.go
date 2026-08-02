package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type User struct {
	ID          string
	DisplayName string
	Email       *string
	Telegram    *string
	Note        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (store *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, display_name, email, telegram, note, created_at, updated_at
FROM users ORDER BY display_name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	values := make([]User, 0)
	for rows.Next() {
		value, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return values, nil
}

func (store *Store) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(store.db.QueryRowContext(ctx, `
SELECT id, display_name, email, telegram, note, created_at, updated_at
FROM users WHERE id = ?`, id))
}

func (store *Store) CreateUser(ctx context.Context, value User, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user creation: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
INSERT INTO users(id, display_name, email, telegram, note, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, value.ID, value.DisplayName, value.Email, value.Telegram, value.Note, unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read user creation result: %w", err)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "user.create", TargetType: "user", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "user creation")
}

func (store *Store) UpdateUser(ctx context.Context, value User, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE OR IGNORE users SET display_name = ?, email = ?, telegram = ?, note = ?, updated_at = ? WHERE id = ?`,
		value.DisplayName, value.Email, value.Telegram, value.Note, unixTime(now), value.ID)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read user update result: %w", err)
	}
	if updated != 1 {
		exists, err := existsTx(ctx, tx, "users", value.ID)
		if err != nil {
			return err
		}
		if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "user.update", TargetType: "user", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "user update")
}

func (store *Store) DeleteUser(ctx context.Context, id string, now time.Time) error {
	return store.deleteEntity(ctx, "users", "user", id, "user.delete", now)
}

func scanUser(row rowScanner) (User, error) {
	var value User
	var email, telegram sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.DisplayName, &email, &telegram, &value.Note, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	value.Email = nullableString(email)
	value.Telegram = nullableString(telegram)
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
