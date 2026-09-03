package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
)

const (
	DNSProviderSecretOwnerType = "dns_provider_connection"
	DNSProviderTokenSecret     = "api_token"
)

type NodePublicAddress struct {
	NodeID     string
	Family     string
	Address    string
	ObservedAt time.Time
	ReceivedAt time.Time
}

type DNSProviderConnection struct {
	ID        string
	Name      string
	Provider  string
	HasToken  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PublicPortOverrides map[string]map[string]uint16

type NodeEndpoint struct {
	ID                      string
	NodeID                  string
	DisplayName             string
	Kind                    string
	Enabled                 bool
	SourceFamily            string
	Address                 string
	PublicPortOverrides     PublicPortOverrides
	DNSProviderConnectionID *string
	ZoneName                string
	RecordName              string
	TTL                     int
	Proxied                 bool
	SyncStatus              string
	ActualAddress           string
	SyncError               string
	SyncedAt                *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type ManagedDDNSEndpoint struct {
	Endpoint NodeEndpoint
	Provider DNSProviderConnection
	Desired  *NodePublicAddress
}

func (store *Store) ListNodePublicAddresses(ctx context.Context, nodeID string) ([]NodePublicAddress, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT node_id, family, address, observed_at_ns, received_at
FROM node_public_addresses WHERE node_id = ? ORDER BY family`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node public addresses: %w", err)
	}
	defer rows.Close()
	values := make([]NodePublicAddress, 0, 2)
	for rows.Next() {
		var value NodePublicAddress
		var observedAt, receivedAt int64
		if err := rows.Scan(&value.NodeID, &value.Family, &value.Address, &observedAt, &receivedAt); err != nil {
			return nil, fmt.Errorf("scan node public address: %w", err)
		}
		value.ObservedAt = time.Unix(0, observedAt).UTC()
		value.ReceivedAt = fromUnix(receivedAt)
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *Store) RecordNodePublicAddresses(ctx context.Context, nodeID string, value agentv1.PublicAddressesEvent,
	observedAt, receivedAt time.Time,
) error {
	if err := agentv1.ValidatePublicAddressesEvent(value); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin public address observation: %w", err)
	}
	defer tx.Rollback()
	nodeExists, err := existsTx(ctx, tx, "nodes", nodeID)
	if err != nil {
		return err
	}
	if !nodeExists {
		return nil
	}
	for _, address := range value.Addresses {
		result, err := tx.ExecContext(ctx, `
INSERT INTO node_public_addresses(node_id, family, address, observed_at_ns, received_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(node_id, family) DO UPDATE SET
    address = excluded.address,
    observed_at_ns = excluded.observed_at_ns,
    received_at = excluded.received_at
WHERE excluded.observed_at_ns >= node_public_addresses.observed_at_ns`,
			nodeID, address.Family, address.Address, observedAt.UnixNano(), unixTime(receivedAt))
		if err != nil {
			return fmt.Errorf("record node public address: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read public address observation result: %w", err)
		}
		if changed == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE node_endpoints SET sync_status = 'pending', sync_error = '', updated_at = ?
WHERE node_id = ? AND kind = 'managed_ddns' AND source_family = ?
  AND (actual_address <> ? OR sync_status <> 'synced')`,
			unixTime(receivedAt), nodeID, address.Family, address.Address); err != nil {
			return fmt.Errorf("mark managed DDNS endpoints pending: %w", err)
		}
	}
	return commit(tx, "public address observation")
}

func (store *Store) ListDNSProviderConnections(ctx context.Context) ([]DNSProviderConnection, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT connections.id, connections.name, connections.provider,
       EXISTS (SELECT 1 FROM secrets WHERE owner_type = ? AND owner_id = connections.id AND name = ?),
       connections.created_at, connections.updated_at
FROM dns_provider_connections connections ORDER BY connections.name COLLATE NOCASE, connections.id`,
		DNSProviderSecretOwnerType, DNSProviderTokenSecret)
	if err != nil {
		return nil, fmt.Errorf("list DNS provider connections: %w", err)
	}
	defer rows.Close()
	values := make([]DNSProviderConnection, 0)
	for rows.Next() {
		value, err := scanDNSProviderConnection(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *Store) DNSProviderConnectionByID(ctx context.Context, id string) (DNSProviderConnection, error) {
	return scanDNSProviderConnection(store.db.QueryRowContext(ctx, `
SELECT connections.id, connections.name, connections.provider,
       EXISTS (SELECT 1 FROM secrets WHERE owner_type = ? AND owner_id = connections.id AND name = ?),
       connections.created_at, connections.updated_at
FROM dns_provider_connections connections WHERE connections.id = ?`,
		DNSProviderSecretOwnerType, DNSProviderTokenSecret, id))
}

func (store *Store) CreateDNSProviderConnection(ctx context.Context, value DNSProviderConnection, tokenCiphertext []byte, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin DNS provider connection creation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO dns_provider_connections(id, name, provider, created_at, updated_at)
VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, value.ID, value.Name, value.Provider, unixTime(now), unixTime(now))
	if err != nil {
		return fmt.Errorf("insert DNS provider connection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at) VALUES (?, ?, ?, ?, ?)`,
		DNSProviderSecretOwnerType, value.ID, DNSProviderTokenSecret, tokenCiphertext, unixTime(now)); err != nil {
		return fmt.Errorf("store DNS provider token: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1",
		Action: "dns_provider_connection.create", TargetType: "dns_provider_connection", TargetID: value.ID, Outcome: "success",
		Metadata: map[string]any{"provider": value.Provider}}); err != nil {
		return err
	}
	return commit(tx, "DNS provider connection creation")
}

func (store *Store) UpdateDNSProviderConnection(ctx context.Context, value DNSProviderConnection, tokenCiphertext []byte, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin DNS provider connection update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE OR IGNORE dns_provider_connections SET name = ?, updated_at = ? WHERE id = ? AND provider = ?`,
		value.Name, unixTime(now), value.ID, value.Provider)
	if err != nil {
		return fmt.Errorf("update DNS provider connection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		if exists, checkErr := existsTx(ctx, tx, "dns_provider_connections", value.ID); checkErr != nil {
			return checkErr
		} else if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	if tokenCiphertext != nil {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
			DNSProviderSecretOwnerType, value.ID, DNSProviderTokenSecret, tokenCiphertext, unixTime(now)); err != nil {
			return fmt.Errorf("replace DNS provider token: %w", err)
		}
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1",
		Action: "dns_provider_connection.update", TargetType: "dns_provider_connection", TargetID: value.ID, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "DNS provider connection update")
}

func (store *Store) DeleteDNSProviderConnection(ctx context.Context, id string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin DNS provider connection deletion: %w", err)
	}
	defer tx.Rollback()
	var references int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM node_endpoints WHERE dns_provider_connection_id = ?`, id).Scan(&references); err != nil {
		return fmt.Errorf("count DNS provider connection references: %w", err)
	}
	if references > 0 {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM dns_provider_connections WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete DNS provider connection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1",
		Action: "dns_provider_connection.delete", TargetType: "dns_provider_connection", TargetID: id, Outcome: "success"}); err != nil {
		return err
	}
	return commit(tx, "DNS provider connection deletion")
}

func (store *Store) ListNodeEndpoints(ctx context.Context, nodeID string) ([]NodeEndpoint, error) {
	rows, err := store.db.QueryContext(ctx, nodeEndpointSelect+` WHERE endpoints.node_id = ? ORDER BY endpoints.display_name COLLATE NOCASE, endpoints.id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list node endpoints: %w", err)
	}
	defer rows.Close()
	values := make([]NodeEndpoint, 0)
	for rows.Next() {
		value, err := scanNodeEndpoint(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *Store) NodeEndpointByID(ctx context.Context, nodeID, id string) (NodeEndpoint, error) {
	return scanNodeEndpoint(store.db.QueryRowContext(ctx, nodeEndpointSelect+` WHERE endpoints.node_id = ? AND endpoints.id = ?`, nodeID, id))
}

func (store *Store) NodeEndpointByGlobalID(ctx context.Context, id string) (NodeEndpoint, error) {
	return scanNodeEndpoint(store.db.QueryRowContext(ctx, nodeEndpointSelect+` WHERE endpoints.id = ?`, id))
}

func (store *Store) CreateNodeEndpoint(ctx context.Context, value NodeEndpoint, now time.Time) error {
	return store.writeNodeEndpoint(ctx, value, true, now)
}

func (store *Store) UpdateNodeEndpoint(ctx context.Context, value NodeEndpoint, now time.Time) error {
	return store.writeNodeEndpoint(ctx, value, false, now)
}

func (store *Store) writeNodeEndpoint(ctx context.Context, value NodeEndpoint, create bool, now time.Time) error {
	ports, err := json.Marshal(value.PublicPortOverrides)
	if err != nil {
		return fmt.Errorf("encode public port overrides: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node endpoint write: %w", err)
	}
	defer tx.Rollback()
	var result sql.Result
	if create {
		result, err = tx.ExecContext(ctx, `
INSERT INTO node_endpoints(id, node_id, display_name, kind, enabled, source_family, address,
    public_port_overrides_json, dns_provider_connection_id, zone_name, record_name,
    ttl, proxied, sync_status, actual_address, sync_error, synced_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			value.ID, value.NodeID, value.DisplayName, value.Kind, boolInt(value.Enabled), value.SourceFamily, value.Address,
			string(ports), value.DNSProviderConnectionID, value.ZoneName, value.RecordName,
			value.TTL, boolInt(value.Proxied), value.SyncStatus, value.ActualAddress, value.SyncError,
			nullableUnix(value.SyncedAt), unixTime(now), unixTime(now))
	} else {
		result, err = tx.ExecContext(ctx, `
UPDATE OR IGNORE node_endpoints SET display_name = ?, kind = ?, enabled = ?, source_family = ?, address = ?,
    public_port_overrides_json = ?, dns_provider_connection_id = ?, zone_name = ?, record_name = ?,
    ttl = ?, proxied = ?, sync_status = ?, actual_address = ?, sync_error = ?, synced_at = ?, updated_at = ?
WHERE id = ? AND node_id = ?`,
			value.DisplayName, value.Kind, boolInt(value.Enabled), value.SourceFamily, value.Address, string(ports),
			value.DNSProviderConnectionID, value.ZoneName, value.RecordName, value.TTL, boolInt(value.Proxied), value.SyncStatus,
			value.ActualAddress, value.SyncError, nullableUnix(value.SyncedAt), unixTime(now), value.ID, value.NodeID)
	}
	if err != nil {
		return fmt.Errorf("write node endpoint: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		if create {
			return ErrConflict
		}
		if exists, checkErr := existsTx(ctx, tx, "node_endpoints", value.ID); checkErr != nil {
			return checkErr
		} else if exists {
			return ErrConflict
		}
		return ErrNotFound
	}
	action := "node_endpoint.update"
	if create {
		action = "node_endpoint.create"
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: action,
		TargetType: "node_endpoint", TargetID: value.ID, Outcome: "success", Metadata: map[string]any{"node_id": value.NodeID, "kind": value.Kind}}); err != nil {
		return err
	}
	return commit(tx, "node endpoint write")
}

func (store *Store) DeleteNodeEndpoint(ctx context.Context, nodeID, id string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin node endpoint deletion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM node_endpoints WHERE id = ? AND node_id = ?`, id, nodeID)
	if err != nil {
		return fmt.Errorf("delete node endpoint: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{OccurredAt: now, ActorType: "administrator", ActorID: "1",
		Action: "node_endpoint.delete", TargetType: "node_endpoint", TargetID: id, Outcome: "success", Metadata: map[string]any{"node_id": nodeID}}); err != nil {
		return err
	}
	return commit(tx, "node endpoint deletion")
}

func (store *Store) ListManagedDDNSEndpoints(ctx context.Context) ([]ManagedDDNSEndpoint, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT id, node_id FROM node_endpoints
WHERE kind = 'managed_ddns' AND enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list managed DDNS endpoints: %w", err)
	}
	type endpointKey struct{ id, nodeID string }
	keys := make([]endpointKey, 0)
	for rows.Next() {
		var key endpointKey
		if err := rows.Scan(&key.id, &key.nodeID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan managed DDNS endpoint key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close managed DDNS endpoint keys: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed DDNS endpoint keys: %w", err)
	}
	values := make([]ManagedDDNSEndpoint, 0, len(keys))
	for _, key := range keys {
		endpoint, err := store.NodeEndpointByID(ctx, key.nodeID, key.id)
		if err != nil {
			return nil, err
		}
		provider, err := store.DNSProviderConnectionByID(ctx, *endpoint.DNSProviderConnectionID)
		if err != nil {
			return nil, err
		}
		value := ManagedDDNSEndpoint{Endpoint: endpoint, Provider: provider}
		var observed NodePublicAddress
		var observedAt, receivedAt int64
		err = store.db.QueryRowContext(ctx, `
SELECT node_id, family, address, observed_at_ns, received_at
FROM node_public_addresses WHERE node_id = ? AND family = ?`, endpoint.NodeID, endpoint.SourceFamily).Scan(
			&observed.NodeID, &observed.Family, &observed.Address, &observedAt, &receivedAt)
		if err == nil {
			observed.ObservedAt, observed.ReceivedAt = time.Unix(0, observedAt).UTC(), fromUnix(receivedAt)
			value.Desired = &observed
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read managed DDNS desired address: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) RecordDDNSSync(ctx context.Context, endpointID, status, actualAddress, problem string, syncedAt, now time.Time) error {
	var synced any
	if !syncedAt.IsZero() {
		synced = unixTime(syncedAt)
	}
	result, err := store.db.ExecContext(ctx, `
UPDATE node_endpoints SET sync_status = ?, actual_address = ?, sync_error = ?, synced_at = ?, updated_at = ? WHERE id = ?`,
		status, actualAddress, problem, synced, unixTime(now), endpointID)
	if err != nil {
		return fmt.Errorf("record DDNS sync: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) AppendSystemAudit(ctx context.Context, entry AuditEntry) error {
	return store.AppendAudit(ctx, entry)
}

const nodeEndpointSelect = `
SELECT endpoints.id, endpoints.node_id, endpoints.display_name, endpoints.kind, endpoints.enabled,
       endpoints.source_family, endpoints.address, endpoints.public_port_overrides_json,
       endpoints.dns_provider_connection_id, endpoints.zone_name, endpoints.record_name, endpoints.ttl,
       endpoints.proxied, endpoints.sync_status, endpoints.actual_address, endpoints.sync_error,
       endpoints.synced_at, endpoints.created_at, endpoints.updated_at
FROM node_endpoints endpoints`

func scanNodeEndpoint(row rowScanner) (NodeEndpoint, error) {
	var value NodeEndpoint
	var enabled, proxied int
	var ports []byte
	var providerID sql.NullString
	var syncedAt sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.NodeID, &value.DisplayName, &value.Kind, &enabled,
		&value.SourceFamily, &value.Address, &ports, &providerID, &value.ZoneName, &value.RecordName,
		&value.TTL, &proxied, &value.SyncStatus, &value.ActualAddress, &value.SyncError, &syncedAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeEndpoint{}, ErrNotFound
		}
		return NodeEndpoint{}, fmt.Errorf("scan node endpoint: %w", err)
	}
	value.Enabled, value.Proxied = enabled != 0, proxied != 0
	if providerID.Valid {
		value.DNSProviderConnectionID = &providerID.String
	}
	if err := json.Unmarshal(ports, &value.PublicPortOverrides); err != nil {
		return NodeEndpoint{}, fmt.Errorf("decode public port overrides: %w", err)
	}
	if value.PublicPortOverrides == nil {
		value.PublicPortOverrides = PublicPortOverrides{}
	}
	value.SyncedAt = nullableTime(syncedAt)
	value.CreatedAt, value.UpdatedAt = fromUnix(createdAt), fromUnix(updatedAt)
	return value, nil
}

func scanDNSProviderConnection(row rowScanner) (DNSProviderConnection, error) {
	var value DNSProviderConnection
	var hasToken int
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &value.Provider, &hasToken, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DNSProviderConnection{}, ErrNotFound
		}
		return DNSProviderConnection{}, fmt.Errorf("scan DNS provider connection: %w", err)
	}
	value.HasToken = hasToken != 0
	value.CreatedAt, value.UpdatedAt = fromUnix(createdAt), fromUnix(updatedAt)
	return value, nil
}
