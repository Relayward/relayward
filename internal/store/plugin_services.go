package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PluginService struct {
	NodeID             string
	PluginID           string
	ServiceID          string
	DisplayName        string
	Enabled            bool
	Capabilities       []string
	SubscriptionSHA256 string
	UpdatedAt          time.Time
}

func (store *Store) ReplacePluginServices(ctx context.Context, pluginID, nodeID string, services []PluginService, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin service replacement: %w", err)
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `
SELECT 1 FROM node_plugin_instances WHERE node_id = ? AND plugin_id = ?`, nodeID, pluginID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check node plugin instance for services: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_services WHERE node_id = ? AND plugin_id = ?", nodeID, pluginID); err != nil {
		return fmt.Errorf("clear plugin services: %w", err)
	}
	for _, service := range services {
		capabilities, err := json.Marshal(service.Capabilities)
		if err != nil {
			return fmt.Errorf("encode plugin service capabilities: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_services(
    node_id, plugin_id, service_id, display_name, enabled, capabilities_json, subscription_sha256, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, nodeID, pluginID, service.ServiceID, service.DisplayName,
			boolInt(service.Enabled), capabilities, service.SubscriptionSHA256, unixTime(now)); err != nil {
			return fmt.Errorf("insert plugin service: %w", err)
		}
	}
	pruned, err := tx.ExecContext(ctx, `
DELETE FROM service_bindings
WHERE plugin_id = ?
  AND authorization_id IN (SELECT id FROM authorizations WHERE node_id = ?)
  AND NOT EXISTS (
      SELECT 1 FROM plugin_services current
      WHERE current.node_id = ?
        AND current.plugin_id = service_bindings.plugin_id
        AND current.service_id = service_bindings.service_id
  )`, pluginID, nodeID, nodeID)
	if err != nil {
		return fmt.Errorf("prune removed plugin service bindings: %w", err)
	}
	prunedCount, err := pruned.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pruned service binding count: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "plugin", ActorID: pluginID, Action: "plugin_services.replace",
		TargetType: "node_plugin_instance", TargetID: nodeID + "/" + pluginID, Outcome: "success",
		Metadata: map[string]any{"service_count": len(services), "pruned_binding_count": prunedCount},
	}); err != nil {
		return err
	}
	return commit(tx, "plugin service replacement")
}

func (store *Store) ListPluginServices(ctx context.Context, nodeID string) ([]PluginService, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT node_id, plugin_id, service_id, display_name, enabled, capabilities_json,
       subscription_sha256, updated_at
FROM plugin_services
WHERE (? = '' OR node_id = ?)
ORDER BY node_id, plugin_id, service_id`, nodeID, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list plugin services: %w", err)
	}
	defer rows.Close()
	values := make([]PluginService, 0)
	for rows.Next() {
		var value PluginService
		var enabled int
		var capabilities []byte
		var updatedAt int64
		if err := rows.Scan(&value.NodeID, &value.PluginID, &value.ServiceID, &value.DisplayName, &enabled,
			&capabilities, &value.SubscriptionSHA256, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan plugin service: %w", err)
		}
		if err := json.Unmarshal(capabilities, &value.Capabilities); err != nil {
			return nil, fmt.Errorf("decode plugin service capabilities: %w", err)
		}
		if value.Capabilities == nil {
			value.Capabilities = []string{}
		}
		value.Enabled = enabled != 0
		value.UpdatedAt = fromUnix(updatedAt)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plugin services: %w", err)
	}
	return values, nil
}
