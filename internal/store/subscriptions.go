package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	policyv1 "github.com/Relayward/relayward-sdk/policy/v1"
)

const (
	SubscriptionFormatBase64  = "base64"
	SubscriptionFormatMihomo  = "mihomo"
	SubscriptionFormatSingBox = "sing-box"
)

var subscriptionFormats = []string{SubscriptionFormatBase64, SubscriptionFormatMihomo, SubscriptionFormatSingBox}

type SubscriptionService struct {
	PluginID             string
	ServiceID            string
	DisplayName          string
	Capabilities         []string
	SubscriptionSHA256   string
	PluginVersion        string
	PluginState          string
	PluginHealth         string
	NodeDesiredState     string
	NodeActualState      string
	NodeGeneration       uint64
	NodeConfigurationSHA string
}

type SubscriptionSnapshot struct {
	Authorization    Authorization
	Settings         SystemSettings
	UserName         string
	NodeName         string
	NodeEnabled      bool
	TrafficUsedBytes *uint64
	Announcement     *string
	Services         []SubscriptionService
}

type SubscriptionRenderCache struct {
	InputSHA256 string
	Base64      []byte
	Mihomo      []byte
	SingBox     []byte
	RenderedAt  time.Time
}

func (store *Store) SubscriptionByTokenHash(ctx context.Context, tokenHash []byte, at time.Time) (SubscriptionSnapshot, error) {
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("begin subscription snapshot: %w", err)
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
SELECT a.id, a.user_id, a.node_id, a.enabled, a.traffic_limit_bytes, a.reset_kind, a.reset_value, a.timezone,
       a.period_anchor, a.expires_at, a.soft_ip_limit, a.activity_window_seconds, a.block_duration_seconds,
       a.subscription_token_hash, a.created_at, a.updated_at,
       u.display_name, n.name, n.enabled
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
		&snapshot.UserName, &snapshot.NodeName, &nodeEnabled,
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

	reset := policyv1.ResetRule{
		Kind: snapshot.Authorization.ResetKind, Timezone: snapshot.Authorization.Timezone,
		PeriodAnchor: snapshot.Authorization.PeriodAnchor,
	}
	if snapshot.Authorization.ResetValue != nil {
		value := uint32(*snapshot.Authorization.ResetValue)
		reset.Value = &value
	}
	period, err := policyv1.CurrentPeriod(reset, snapshot.Authorization.CreatedAt, at.UTC())
	if err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("compute subscription traffic period: %w", err)
	}
	var uploadBytes, downloadBytes int64
	err = tx.QueryRowContext(ctx, `
SELECT upload_bytes, download_bytes FROM traffic_periods
WHERE authorization_id = ? AND period_id = ?`, snapshot.Authorization.ID, period.ID).Scan(&uploadBytes, &downloadBytes)
	if err == nil {
		used := uint64(uploadBytes) + uint64(downloadBytes)
		snapshot.TrafficUsedBytes = &used
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SubscriptionSnapshot{}, fmt.Errorf("read subscription traffic: %w", err)
	}

	var announcement string
	err = tx.QueryRowContext(ctx, "SELECT content FROM announcements WHERE id = 1").Scan(&announcement)
	if err == nil && announcement != "" {
		snapshot.Announcement = &announcement
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SubscriptionSnapshot{}, fmt.Errorf("read subscription announcement: %w", err)
	}

	var settingsUpdatedAt int64
	if err := tx.QueryRowContext(ctx, `
SELECT session_lifetime_minutes, timezone, public_url, subscription_title,
       support_url, profile_url, subscription_refresh_hours, updated_at
FROM system_settings WHERE id = 1`).Scan(
		&snapshot.Settings.SessionLifetimeMinutes, &snapshot.Settings.Timezone, &snapshot.Settings.PublicURL,
		&snapshot.Settings.SubscriptionTitle, &snapshot.Settings.SupportURL, &snapshot.Settings.ProfileURL,
		&snapshot.Settings.SubscriptionRefreshHours, &settingsUpdatedAt,
	); err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("read subscription settings: %w", err)
	}
	snapshot.Settings.UpdatedAt = fromUnix(settingsUpdatedAt)

	rows, err := tx.QueryContext(ctx, `
SELECT bindings.plugin_id, bindings.service_id, services.display_name, services.capabilities_json,
       services.subscription_sha256, installations.active_version, installations.state, installations.health,
       instances.desired_state, instances.actual_state, instances.generation,
       instances.desired_configuration_sha256
FROM service_bindings bindings
JOIN plugin_services services
  ON services.node_id = ? AND services.plugin_id = bindings.plugin_id
 AND services.service_id = bindings.service_id
JOIN plugin_installations installations ON installations.plugin_id = bindings.plugin_id
JOIN node_plugin_instances instances
  ON instances.node_id = ? AND instances.plugin_id = bindings.plugin_id
WHERE bindings.authorization_id = ? AND bindings.enabled = 1 AND services.enabled = 1
ORDER BY bindings.plugin_id, bindings.service_id`, snapshot.Authorization.NodeID, snapshot.Authorization.NodeID, snapshot.Authorization.ID)
	if err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("read subscription services: %w", err)
	}
	snapshot.Services = make([]SubscriptionService, 0)
	for rows.Next() {
		var service SubscriptionService
		var capabilities []byte
		var generation int64
		if err := rows.Scan(&service.PluginID, &service.ServiceID, &service.DisplayName, &capabilities,
			&service.SubscriptionSHA256, &service.PluginVersion, &service.PluginState, &service.PluginHealth,
			&service.NodeDesiredState, &service.NodeActualState, &generation, &service.NodeConfigurationSHA); err != nil {
			rows.Close()
			return SubscriptionSnapshot{}, fmt.Errorf("scan subscription service: %w", err)
		}
		if err := json.Unmarshal(capabilities, &service.Capabilities); err != nil {
			rows.Close()
			return SubscriptionSnapshot{}, fmt.Errorf("decode subscription service capabilities: %w", err)
		}
		if service.Capabilities == nil {
			service.Capabilities = []string{}
		}
		service.NodeGeneration = uint64(generation)
		snapshot.Services = append(snapshot.Services, service)
	}
	if err := rows.Close(); err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("close subscription services: %w", err)
	}
	if err := rows.Err(); err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("iterate subscription services: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionSnapshot{}, fmt.Errorf("commit subscription snapshot read: %w", err)
	}
	return snapshot, nil
}

func (store *Store) SaveSubscriptionRenderCache(ctx context.Context, authorizationID string,
	value SubscriptionRenderCache,
) error {
	if len(value.InputSHA256) != 64 || value.RenderedAt.IsZero() {
		return errors.New("invalid subscription render cache")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin subscription cache update: %w", err)
	}
	defer tx.Rollback()
	contents := map[string][]byte{
		SubscriptionFormatBase64:  value.Base64,
		SubscriptionFormatMihomo:  value.Mihomo,
		SubscriptionFormatSingBox: value.SingBox,
	}
	for _, format := range subscriptionFormats {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO subscription_render_cache(authorization_id, format, content, rendered_at, input_sha256)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(authorization_id, format) DO UPDATE SET
    content = excluded.content, rendered_at = excluded.rendered_at, input_sha256 = excluded.input_sha256`,
			authorizationID, format, contents[format], unixTime(value.RenderedAt), value.InputSHA256); err != nil {
			return fmt.Errorf("store %s subscription cache: %w", format, err)
		}
	}
	return commit(tx, "subscription cache update")
}

func (store *Store) SubscriptionRenderCache(ctx context.Context, authorizationID, inputSHA256 string) (SubscriptionRenderCache, error) {
	rows, err := store.db.QueryContext(ctx, `
SELECT format, content, rendered_at FROM subscription_render_cache
WHERE authorization_id = ? AND input_sha256 = ?
ORDER BY format`, authorizationID, inputSHA256)
	if err != nil {
		return SubscriptionRenderCache{}, fmt.Errorf("read subscription cache: %w", err)
	}
	defer rows.Close()
	value := SubscriptionRenderCache{InputSHA256: inputSHA256}
	seen := make(map[string]bool, len(subscriptionFormats))
	var renderedAt int64
	for rows.Next() {
		var format string
		var content []byte
		var rowRenderedAt int64
		if err := rows.Scan(&format, &content, &rowRenderedAt); err != nil {
			return SubscriptionRenderCache{}, fmt.Errorf("scan subscription cache: %w", err)
		}
		if renderedAt != 0 && renderedAt != rowRenderedAt {
			return SubscriptionRenderCache{}, ErrNotFound
		}
		renderedAt = rowRenderedAt
		switch format {
		case SubscriptionFormatBase64:
			value.Base64 = append([]byte(nil), content...)
		case SubscriptionFormatMihomo:
			value.Mihomo = append([]byte(nil), content...)
		case SubscriptionFormatSingBox:
			value.SingBox = append([]byte(nil), content...)
		default:
			continue
		}
		seen[format] = true
	}
	if err := rows.Err(); err != nil {
		return SubscriptionRenderCache{}, fmt.Errorf("iterate subscription cache: %w", err)
	}
	for _, format := range subscriptionFormats {
		if !seen[format] {
			return SubscriptionRenderCache{}, ErrNotFound
		}
	}
	value.RenderedAt = fromUnix(renderedAt)
	return value, nil
}

func (store *Store) Announcement(ctx context.Context) (*string, error) {
	var content string
	err := store.db.QueryRowContext(ctx, "SELECT content FROM announcements WHERE id = 1").Scan(&content)
	if errors.Is(err, sql.ErrNoRows) || content == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read announcement: %w", err)
	}
	return &content, nil
}

func (store *Store) UpdateAnnouncement(ctx context.Context, content string, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin announcement update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO announcements(id, content, updated_at) VALUES (1, ?, ?)
ON CONFLICT(id) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`, content, unixTime(now)); err != nil {
		return fmt.Errorf("update announcement: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "announcement.update",
		TargetType: "announcement", TargetID: "1", Outcome: "success",
		Metadata: map[string]any{"configured": content != ""},
	}); err != nil {
		return err
	}
	return commit(tx, "announcement update")
}
