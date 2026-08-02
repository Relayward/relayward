package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ServiceBinding struct {
	ID              string
	AuthorizationID string
	PluginID        string
	ServiceID       string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (store *Store) ListServiceBindings(ctx context.Context, authorizationID string) ([]ServiceBinding, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, authorization_id, plugin_id, service_id, enabled, created_at, updated_at
FROM service_bindings WHERE authorization_id = ? ORDER BY plugin_id, service_id, id`, authorizationID)
	if err != nil {
		return nil, fmt.Errorf("list service bindings: %w", err)
	}
	defer rows.Close()
	values := make([]ServiceBinding, 0)
	for rows.Next() {
		value, err := scanServiceBinding(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate service bindings: %w", err)
	}
	return values, nil
}

func (store *Store) ServiceBindingByID(ctx context.Context, id string) (ServiceBinding, error) {
	return scanServiceBinding(store.db.QueryRowContext(ctx, `
SELECT id, authorization_id, plugin_id, service_id, enabled, created_at, updated_at
FROM service_bindings WHERE id = ?`, id))
}

func (store *Store) CreateServiceBinding(ctx context.Context, value ServiceBinding, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin service binding creation: %w", err)
	}
	defer tx.Rollback()
	var serviceExists int
	err = tx.QueryRowContext(ctx, `
SELECT 1
FROM authorizations a
JOIN plugin_services s ON s.node_id = a.node_id
WHERE a.id = ? AND s.plugin_id = ? AND s.service_id = ?`,
		value.AuthorizationID, value.PluginID, value.ServiceID).Scan(&serviceExists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check plugin service: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO service_bindings(id, authorization_id, plugin_id, service_id, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, value.ID, value.AuthorizationID, value.PluginID, value.ServiceID, boolInt(value.Enabled), unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert service binding: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read service binding creation result: %w", err)
	}
	if inserted != 1 {
		return ErrConflict
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "service_binding.create", TargetType: "service_binding", TargetID: value.ID, Outcome: "success", Metadata: map[string]any{"authorization_id": value.AuthorizationID, "plugin_id": value.PluginID, "service_id": value.ServiceID}}); err != nil {
		return err
	}
	return commit(tx, "service binding creation")
}

func (store *Store) UpdateServiceBinding(ctx context.Context, id string, enabled bool, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin service binding update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE service_bindings SET enabled = ?, updated_at = ? WHERE id = ?", boolInt(enabled), unixTime(now), id)
	if err != nil {
		return fmt.Errorf("update service binding: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read service binding update result: %w", err)
	}
	if updated != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "service_binding.update", TargetType: "service_binding", TargetID: id, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "service binding update")
}

func (store *Store) DeleteServiceBinding(ctx context.Context, id string, now time.Time) error {
	return store.deleteEntity(ctx, "service_bindings", "service_binding", id, "service_binding.delete", now)
}

func scanServiceBinding(row rowScanner) (ServiceBinding, error) {
	var value ServiceBinding
	var enabled int
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.AuthorizationID, &value.PluginID, &value.ServiceID, &enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ServiceBinding{}, ErrNotFound
		}
		return ServiceBinding{}, fmt.Errorf("scan service binding: %w", err)
	}
	value.Enabled = enabled != 0
	value.CreatedAt = fromUnix(createdAt)
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}
