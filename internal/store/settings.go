package store

import (
	"context"
	"fmt"
	"time"
)

type SystemSettings struct {
	SessionLifetimeMinutes   int
	Timezone                 string
	PublicURL                string
	SubscriptionTitle        string
	SupportURL               string
	ProfileURL               string
	SubscriptionRefreshHours int
	UpdatedAt                time.Time
}

func (store *Store) SystemSettings(ctx context.Context) (SystemSettings, error) {
	var value SystemSettings
	var updatedAt int64
	err := store.db.QueryRowContext(ctx, `
SELECT session_lifetime_minutes, timezone, public_url, subscription_title,
       support_url, profile_url, subscription_refresh_hours, updated_at
FROM system_settings WHERE id = 1`).Scan(
		&value.SessionLifetimeMinutes, &value.Timezone, &value.PublicURL, &value.SubscriptionTitle,
		&value.SupportURL, &value.ProfileURL, &value.SubscriptionRefreshHours, &updatedAt,
	)
	if err != nil {
		return SystemSettings{}, fmt.Errorf("read system settings: %w", err)
	}
	value.UpdatedAt = fromUnix(updatedAt)
	return value, nil
}

func (store *Store) UpdateSystemSettings(ctx context.Context, value SystemSettings, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin system settings update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE system_settings SET
    session_lifetime_minutes = ?, timezone = ?, public_url = ?, subscription_title = ?,
    support_url = ?, profile_url = ?, subscription_refresh_hours = ?, updated_at = ?
WHERE id = 1`, value.SessionLifetimeMinutes, value.Timezone, value.PublicURL, value.SubscriptionTitle,
		value.SupportURL, value.ProfileURL, value.SubscriptionRefreshHours, unixTime(now))
	if err != nil {
		return fmt.Errorf("update system settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read system settings update result: %w", err)
	}
	if updated != 1 {
		return ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "system.settings.update",
		TargetType: "system_settings", TargetID: "1", Outcome: "success",
	}); err != nil {
		return err
	}
	return commit(tx, "system settings update")
}
