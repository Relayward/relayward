package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
)

type SecretRecord struct {
	OwnerType  string
	OwnerID    string
	Name       string
	Ciphertext []byte
}

type SecretRecoveryResult struct {
	DiscardedSecrets              int64
	ExpiredCommands               int64
	PluginsRequiringConfiguration int64
}

func (store *Store) CountSecrets(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM secrets").Scan(&count); err != nil {
		return 0, fmt.Errorf("count secrets: %w", err)
	}
	return count, nil
}

func (store *Store) ListSecrets(ctx context.Context) ([]SecretRecord, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT owner_type, owner_id, name, ciphertext
FROM secrets ORDER BY owner_type, owner_id, name`)
	if err != nil {
		return nil, fmt.Errorf("list encrypted secrets: %w", err)
	}
	defer rows.Close()
	values := make([]SecretRecord, 0)
	for rows.Next() {
		var value SecretRecord
		if err := rows.Scan(&value.OwnerType, &value.OwnerID, &value.Name, &value.Ciphertext); err != nil {
			return nil, fmt.Errorf("scan encrypted secret: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted secrets: %w", err)
	}
	return values, nil
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

func (store *Store) DiscardUnrecoverableSecrets(ctx context.Context, now time.Time) (SecretRecoveryResult, error) {
	problem := protocol.Problem{
		Code:      protocol.ErrorUnavailable,
		Message:   "Stored plugin configuration was discarded after instance-key recovery; submit the configuration again.",
		Retryable: false,
	}
	if err := problem.Validate(); err != nil {
		return SecretRecoveryResult{}, err
	}
	problemJSON, err := json.Marshal(problem)
	if err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("encode secret recovery problem: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("begin encrypted secret recovery: %w", err)
	}
	defer tx.Rollback()
	var result SecretRecoveryResult
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM secrets").Scan(&result.DiscardedSecrets); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("count discarded secrets: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM agent_commands WHERE request_encrypted = 1 AND status = 'pending'`).Scan(
		&result.ExpiredCommands,
	); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("count unrecoverable Agent commands: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM node_plugin_instances AS instance
WHERE EXISTS (
    SELECT 1 FROM secrets
    WHERE owner_type = ? AND owner_id = instance.node_id || '/' || instance.plugin_id AND name = ?
)`, NodePluginSecretOwnerType, NodePluginConfigurationSecret).Scan(&result.PluginsRequiringConfiguration); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("count unrecoverable plugin configurations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE administrators SET totp_enabled = 0, totp_last_counter = NULL, updated_at = ? WHERE id = 1`, unixTime(now)); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("disable unrecoverable TOTP: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_codes WHERE administrator_id = 1"); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("delete recovery codes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE administrator_id = 1"); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("revoke administrator sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE agent_commands SET status = 'expired', completed_at = ?, updated_at = ?
WHERE request_encrypted = 1 AND status = 'pending'`, unixTime(now), unixTime(now)); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("expire unrecoverable Agent commands: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE node_plugin_instances AS instance SET
    desired_configuration_sha256 = '', reconcile_status = 'failed', last_problem_json = ?, updated_at = ?
WHERE EXISTS (
    SELECT 1 FROM secrets
    WHERE owner_type = ? AND owner_id = instance.node_id || '/' || instance.plugin_id AND name = ?
)`, string(problemJSON), unixTime(now), NodePluginSecretOwnerType, NodePluginConfigurationSecret); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("mark unrecoverable plugin configurations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM secrets"); err != nil {
		return SecretRecoveryResult{}, fmt.Errorf("discard unrecoverable secrets: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "local_admin", Action: "system.secrets.recover",
		TargetType: "system", TargetID: "1", Outcome: "success",
		Metadata: map[string]any{
			"discarded_secrets":               result.DiscardedSecrets,
			"expired_commands":                result.ExpiredCommands,
			"plugins_requiring_configuration": result.PluginsRequiringConfiguration,
		},
	}); err != nil {
		return SecretRecoveryResult{}, err
	}
	if err := commit(tx, "encrypted secret recovery"); err != nil {
		return SecretRecoveryResult{}, err
	}
	return result, nil
}
