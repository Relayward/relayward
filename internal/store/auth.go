package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Administrator struct {
	ID           int64
	Username     string
	PasswordHash string
	TOTPEnabled  bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	TokenHash       []byte
	CSRFHash        []byte
	AdministratorID int64
	CreatedAt       time.Time
	LastSeenAt      time.Time
	ExpiresAt       time.Time
}

func (store *Store) HasAdministrator(ctx context.Context) (bool, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM administrators").Scan(&count); err != nil {
		return false, fmt.Errorf("count administrators: %w", err)
	}
	return count > 0, nil
}

func (store *Store) InitializeAdministrator(ctx context.Context, username, passwordHash string, now time.Time) (Administrator, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Administrator{}, fmt.Errorf("begin administrator initialization: %w", err)
	}
	defer tx.Rollback()

	timestamp := unixTime(now)
	result, err := tx.ExecContext(ctx, `
INSERT INTO administrators(id, username, password_hash, created_at, updated_at)
SELECT 1, ?, ?, ?, ?
WHERE NOT EXISTS (SELECT 1 FROM administrators)`, username, passwordHash, timestamp, timestamp)
	if err != nil {
		return Administrator{}, fmt.Errorf("insert administrator: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Administrator{}, fmt.Errorf("read administrator initialization result: %w", err)
	}
	if inserted != 1 {
		return Administrator{}, ErrAlreadyInitialized
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now,
		ActorType:  "system",
		Action:     "administrator.initialize",
		TargetType: "administrator",
		TargetID:   "1",
		Outcome:    "success",
	}); err != nil {
		return Administrator{}, err
	}
	if err := tx.Commit(); err != nil {
		return Administrator{}, fmt.Errorf("commit administrator initialization: %w", err)
	}
	return Administrator{
		ID:           1,
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    now.UTC(),
		UpdatedAt:    now.UTC(),
	}, nil
}

func (store *Store) AdministratorByUsername(ctx context.Context, username string) (Administrator, error) {
	return scanAdministrator(store.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, totp_enabled, created_at, updated_at
FROM administrators WHERE username = ? COLLATE NOCASE`, username))
}

func (store *Store) AdministratorByID(ctx context.Context, id int64) (Administrator, error) {
	return scanAdministrator(store.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, totp_enabled, created_at, updated_at
FROM administrators WHERE id = ?`, id))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAdministrator(row rowScanner) (Administrator, error) {
	var value Administrator
	var totpEnabled int
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Username, &value.PasswordHash, &totpEnabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Administrator{}, ErrNotFound
		}
		return Administrator{}, fmt.Errorf("scan administrator: %w", err)
	}
	value.TOTPEnabled = totpEnabled != 0
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) CreateSession(ctx context.Context, value Session) error {
	_, err := store.db.ExecContext(ctx, `
INSERT INTO sessions(token_hash, csrf_hash, administrator_id, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, value.TokenHash, value.CSRFHash, value.AdministratorID,
		unixTime(value.CreatedAt), unixTime(value.LastSeenAt), unixTime(value.ExpiresAt))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (store *Store) CreateSessionWithAudit(ctx context.Context, value Session, entry AuditEntry) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session creation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sessions(token_hash, csrf_hash, administrator_id, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)`, value.TokenHash, value.CSRFHash, value.AdministratorID,
		unixTime(value.CreatedAt), unixTime(value.LastSeenAt), unixTime(value.ExpiresAt)); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	if err := appendAuditTx(ctx, tx, entry); err != nil {
		return err
	}
	return commit(tx, "session creation")
}

func (store *Store) SessionByTokenHash(ctx context.Context, tokenHash []byte, now time.Time) (Session, Administrator, error) {
	row := store.db.QueryRowContext(ctx, `
SELECT s.token_hash, s.csrf_hash, s.administrator_id, s.created_at, s.last_seen_at, s.expires_at,
       a.id, a.username, a.password_hash, a.totp_enabled, a.created_at, a.updated_at
FROM sessions s
JOIN administrators a ON a.id = s.administrator_id
WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, unixTime(now))

	var session Session
	var administrator Administrator
	var sessionCreated, lastSeen, expires int64
	var administratorCreated, administratorUpdated int64
	var totpEnabled int
	if err := row.Scan(&session.TokenHash, &session.CSRFHash, &session.AdministratorID,
		&sessionCreated, &lastSeen, &expires,
		&administrator.ID, &administrator.Username, &administrator.PasswordHash, &totpEnabled,
		&administratorCreated, &administratorUpdated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, Administrator{}, ErrNotFound
		}
		return Session{}, Administrator{}, fmt.Errorf("scan session: %w", err)
	}
	session.CreatedAt = fromUnix(sessionCreated)
	session.LastSeenAt = fromUnix(lastSeen)
	session.ExpiresAt = fromUnix(expires)
	administrator.TOTPEnabled = totpEnabled != 0
	administrator.CreatedAt = fromUnix(administratorCreated)
	administrator.UpdatedAt = fromUnix(administratorUpdated)
	return session, administrator, nil
}

func (store *Store) TouchSession(ctx context.Context, tokenHash []byte, now time.Time) error {
	if _, err := store.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?", unixTime(now), tokenHash); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

func (store *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (store *Store) DeleteSessionWithAudit(ctx context.Context, tokenHash []byte, entry AuditEntry) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session deletion: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := appendAuditTx(ctx, tx, entry); err != nil {
		return err
	}
	return commit(tx, "session deletion")
}

func (store *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", unixTime(now)); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func (store *Store) EnableTOTP(ctx context.Context, ciphertext []byte, recoveryCodeHashes [][]byte, counter int64, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin TOTP enable: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
UPDATE administrators SET totp_enabled = 1, totp_last_counter = ?, updated_at = ?
WHERE id = 1 AND totp_enabled = 0`, counter, unixTime(now))
	if err != nil {
		return fmt.Errorf("enable TOTP: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read TOTP enable result: %w", err)
	}
	if updated != 1 {
		return ErrStateConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES ('administrator', '1', 'totp', ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET
    ciphertext = excluded.ciphertext,
    updated_at = excluded.updated_at`, ciphertext, unixTime(now)); err != nil {
		return fmt.Errorf("store TOTP secret: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE administrator_id = 1"); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	for _, hash := range recoveryCodeHashes {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO recovery_codes(administrator_id, code_hash, created_at) VALUES (1, ?, ?)`, hash, unixTime(now)); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE owner_type = 'administrator' AND owner_id = '1' AND name = 'totp_pending'"); err != nil {
		return fmt.Errorf("delete pending TOTP secret: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "administrator.totp.enable", TargetType: "administrator", TargetID: "1", Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "TOTP enable")
}

func (store *Store) ReplaceRecoveryCodes(ctx context.Context, recoveryCodeHashes [][]byte, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recovery code replacement: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE administrator_id = 1"); err != nil {
		return fmt.Errorf("clear recovery codes: %w", err)
	}
	for _, hash := range recoveryCodeHashes {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO recovery_codes(administrator_id, code_hash, created_at) VALUES (1, ?, ?)`, hash, unixTime(now)); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "administrator.recovery_codes.replace", TargetType: "administrator", TargetID: "1", Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "recovery code replacement")
}

func (store *Store) ResetTOTP(ctx context.Context, actorType string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin TOTP reset: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "UPDATE administrators SET totp_enabled = 0, totp_last_counter = NULL, updated_at = ? WHERE id = 1", unixTime(now)); err != nil {
		return fmt.Errorf("disable TOTP: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE administrator_id = 1"); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM secrets WHERE owner_type = 'administrator' AND owner_id = '1' AND name IN ('totp', 'totp_pending')"); err != nil {
		return fmt.Errorf("delete TOTP secrets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE administrator_id = 1"); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: actorType, Action: "administrator.totp.reset", TargetType: "administrator", TargetID: "1", Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "TOTP reset")
}

func (store *Store) ConsumeRecoveryCode(ctx context.Context, codeHash []byte, now time.Time) (bool, error) {
	result, err := store.db.ExecContext(ctx, `
UPDATE recovery_codes SET used_at = ?
WHERE administrator_id = 1 AND code_hash = ? AND used_at IS NULL`, unixTime(now), codeHash)
	if err != nil {
		return false, fmt.Errorf("consume recovery code: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read recovery code result: %w", err)
	}
	return count == 1, nil
}

func (store *Store) ConsumeTOTPCounter(ctx context.Context, counter int64, now time.Time) (bool, error) {
	result, err := store.db.ExecContext(ctx, `
UPDATE administrators
SET totp_last_counter = ?, updated_at = ?
WHERE id = 1 AND totp_enabled = 1 AND (totp_last_counter IS NULL OR totp_last_counter < ?)`, counter, unixTime(now), counter)
	if err != nil {
		return false, fmt.Errorf("consume TOTP counter: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read TOTP counter result: %w", err)
	}
	return count == 1, nil
}

func commit(tx *sql.Tx, operation string) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}
