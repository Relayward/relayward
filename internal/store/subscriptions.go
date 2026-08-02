package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type SubscriptionSnapshot struct {
	Authorization Authorization
	UserName      string
	NodeName      string
	NodeAddress   string
	NodeEnabled   bool
}

func (store *Store) SubscriptionByTokenHash(ctx context.Context, tokenHash []byte) (SubscriptionSnapshot, error) {
	row := store.db.QueryRowContext(ctx, `
SELECT a.id, a.user_id, a.node_id, a.enabled, a.traffic_limit_bytes, a.reset_kind, a.reset_value, a.timezone,
       a.period_anchor, a.expires_at, a.soft_ip_limit, a.activity_window_seconds, a.block_duration_seconds,
       a.subscription_token_hash, a.created_at, a.updated_at,
       u.display_name, n.name, n.public_address, n.enabled
FROM authorizations a
JOIN users u ON u.id = a.user_id
JOIN nodes n ON n.id = a.node_id
WHERE a.subscription_token_hash = ?`, tokenHash)
	var snapshot SubscriptionSnapshot
	var authorizationEnabled, nodeEnabled int
	var trafficLimit, resetValue, periodAnchor, expiresAt, softIPLimit sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(
		&snapshot.Authorization.ID, &snapshot.Authorization.UserID, &snapshot.Authorization.NodeID, &authorizationEnabled,
		&trafficLimit, &snapshot.Authorization.ResetKind, &resetValue, &snapshot.Authorization.Timezone,
		&periodAnchor, &expiresAt, &softIPLimit, &snapshot.Authorization.ActivityWindowSeconds,
		&snapshot.Authorization.BlockDurationSeconds, &snapshot.Authorization.SubscriptionTokenHash, &createdAt, &updatedAt,
		&snapshot.UserName, &snapshot.NodeName, &snapshot.NodeAddress, &nodeEnabled,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubscriptionSnapshot{}, ErrNotFound
		}
		return SubscriptionSnapshot{}, fmt.Errorf("scan subscription: %w", err)
	}
	snapshot.Authorization.Enabled = authorizationEnabled != 0
	snapshot.Authorization.TrafficLimitBytes = nullableInt64(trafficLimit)
	snapshot.Authorization.ResetValue = nullableInt(resetValue)
	snapshot.Authorization.PeriodAnchor = nullableTime(periodAnchor)
	snapshot.Authorization.ExpiresAt = nullableTime(expiresAt)
	snapshot.Authorization.SoftIPLimit = nullableInt(softIPLimit)
	snapshot.Authorization.CreatedAt = fromUnix(createdAt)
	snapshot.Authorization.UpdatedAt = fromUnix(updatedAt)
	snapshot.NodeEnabled = nodeEnabled != 0
	return snapshot, nil
}
