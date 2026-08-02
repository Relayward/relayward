package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (store *Store) CountSecrets(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM secrets").Scan(&count); err != nil {
		return 0, fmt.Errorf("count secrets: %w", err)
	}
	return count, nil
}

func (store *Store) PutSecret(ctx context.Context, ownerType, ownerID, name string, ciphertext []byte, now time.Time) error {
	_, err := store.db.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET
    ciphertext = excluded.ciphertext,
    updated_at = excluded.updated_at`, ownerType, ownerID, name, ciphertext, unixTime(now))
	if err != nil {
		return fmt.Errorf("put secret: %w", err)
	}
	return nil
}

func (store *Store) Secret(ctx context.Context, ownerType, ownerID, name string) ([]byte, error) {
	var ciphertext []byte
	if err := store.db.QueryRowContext(ctx, `
SELECT ciphertext FROM secrets WHERE owner_type = ? AND owner_id = ? AND name = ?`, ownerType, ownerID, name).Scan(&ciphertext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get secret: %w", err)
	}
	return ciphertext, nil
}

func (store *Store) DeleteSecret(ctx context.Context, ownerType, ownerID, name string) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM secrets WHERE owner_type = ? AND owner_id = ? AND name = ?", ownerType, ownerID, name); err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}
	return nil
}
