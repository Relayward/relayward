package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/Relayward/relayward-sdk/protocol"
)

type PluginVersion struct {
	PluginID            string
	Version             string
	ReleaseID           int64
	ReleaseTag          string
	Manifest            manifest.Manifest
	ApprovedPermissions []string
	CenterAssetID       int64
	NodeAssetID         *int64
	UIAssetID           *int64
	InstalledAt         time.Time
}

func (store *Store) CommitPluginRelease(ctx context.Context, installation PluginInstallation, version PluginVersion,
	githubTokenCiphertext []byte, now time.Time,
) (PluginInstallation, error) {
	if err := validateCommittedPlugin(installation, version); err != nil {
		return PluginInstallation{}, err
	}
	manifestJSON, err := json.Marshal(version.Manifest)
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("encode plugin release manifest: %w", err)
	}
	permissionsJSON, err := json.Marshal(version.Manifest.Permissions)
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("encode plugin release permissions: %w", err)
	}
	approvedJSON, err := json.Marshal(version.ApprovedPermissions)
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("encode approved plugin permissions: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("begin plugin release commit: %w", err)
	}
	defer tx.Rollback()
	var previousRepository string
	var previousActive sql.NullString
	existed := true
	err = tx.QueryRowContext(ctx, `SELECT repository, active_version FROM plugin_installations WHERE plugin_id = ?`,
		installation.PluginID).Scan(&previousRepository, &previousActive)
	if errors.Is(err, sql.ErrNoRows) {
		existed = false
	} else if err != nil {
		return PluginInstallation{}, fmt.Errorf("read existing plugin installation: %w", err)
	} else if previousRepository != installation.Repository {
		return PluginInstallation{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO plugin_installations(
    plugin_id, repository, kind, desired_version, active_version, manifest_json,
    permissions_json, approved_permissions_json, state, previous_version, release_id,
    health, restart_count, last_problem_json, last_started_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', NULL, ?, 'healthy', 0, NULL, ?, ?, ?)
ON CONFLICT(plugin_id) DO UPDATE SET
    kind = excluded.kind,
    desired_version = excluded.desired_version,
    previous_version = CASE
        WHEN plugin_installations.active_version IS NOT NULL AND plugin_installations.active_version <> excluded.active_version
        THEN plugin_installations.active_version ELSE plugin_installations.previous_version END,
    active_version = excluded.active_version,
    manifest_json = excluded.manifest_json,
    permissions_json = excluded.permissions_json,
    approved_permissions_json = excluded.approved_permissions_json,
    state = 'active', release_id = excluded.release_id, health = 'healthy', restart_count = 0,
    last_problem_json = NULL, last_started_at = excluded.last_started_at, updated_at = excluded.updated_at
WHERE plugin_installations.repository = excluded.repository`,
		installation.PluginID, installation.Repository, installation.Kind, installation.ActiveVersion,
		installation.ActiveVersion, string(manifestJSON), string(permissionsJSON), string(approvedJSON),
		version.ReleaseID, unixTime(now), unixTime(now), unixTime(now))
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("commit plugin installation: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("read plugin installation commit result: %w", err)
	}
	if changed != 1 {
		return PluginInstallation{}, ErrConflict
	}
	inserted, err := tx.ExecContext(ctx, `
INSERT INTO plugin_versions(
    plugin_id, version, release_id, release_tag, manifest_json, approved_permissions_json,
    center_asset_id, node_asset_id, ui_asset_id, installed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`, version.PluginID, version.Version, version.ReleaseID, version.ReleaseTag,
		string(manifestJSON), string(approvedJSON), version.CenterAssetID, version.NodeAssetID,
		version.UIAssetID, unixTime(now))
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("store plugin version: %w", err)
	}
	rows, err := inserted.RowsAffected()
	if err != nil {
		return PluginInstallation{}, fmt.Errorf("read plugin version creation result: %w", err)
	}
	if rows == 0 {
		stored, err := pluginVersionByIDTx(ctx, tx, version.PluginID, version.Version)
		if err != nil {
			return PluginInstallation{}, err
		}
		if !samePluginVersion(stored, version) {
			return PluginInstallation{}, ErrConflict
		}
	}
	if githubTokenCiphertext != nil {
		if len(githubTokenCiphertext) == 0 {
			return PluginInstallation{}, errors.New("encrypted GitHub token must not be empty")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET
    ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
			PluginInstallationSecretOwnerType, installation.PluginID, PluginInstallationGitHubToken,
			githubTokenCiphertext, unixTime(now)); err != nil {
			return PluginInstallation{}, fmt.Errorf("store encrypted GitHub token: %w", err)
		}
	}
	action := "plugin.install"
	metadata := map[string]any{"repository": installation.Repository, "version": installation.ActiveVersion}
	if existed {
		action = "plugin.upgrade"
		metadata["previous_version"] = previousActive.String
	}
	metadata["permissions"] = version.ApprovedPermissions
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: action,
		TargetType: "plugin_installation", TargetID: installation.PluginID, Outcome: "success", Metadata: metadata,
	}); err != nil {
		return PluginInstallation{}, err
	}
	if err := commit(tx, "plugin release commit"); err != nil {
		return PluginInstallation{}, err
	}
	return store.PluginInstallationByID(ctx, installation.PluginID)
}

func (store *Store) PluginVersionByID(ctx context.Context, pluginID, version string) (PluginVersion, error) {
	return scanPluginVersion(store.db.QueryRowContext(ctx, pluginVersionSelect+`
WHERE plugin_id = ? AND version = ?`, pluginID, version))
}

func (store *Store) ReplacePluginGitHubToken(ctx context.Context, pluginID string, ciphertext []byte, now time.Time) error {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return err
	}
	if len(ciphertext) == 0 {
		return errors.New("encrypted GitHub token must not be empty")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin GitHub token replacement: %w", err)
	}
	defer tx.Rollback()
	var repository string
	if err := tx.QueryRowContext(ctx, `SELECT repository FROM plugin_installations WHERE plugin_id = ?`, pluginID).Scan(&repository); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read plugin before GitHub token replacement: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO secrets(owner_type, owner_id, name, ciphertext, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner_type, owner_id, name) DO UPDATE SET
    ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		PluginInstallationSecretOwnerType, pluginID, PluginInstallationGitHubToken, ciphertext, unixTime(now)); err != nil {
		return fmt.Errorf("replace encrypted plugin GitHub token: %w", err)
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "plugin.github_token.replace",
		TargetType: "plugin_installation", TargetID: pluginID, Outcome: "success",
		Metadata: map[string]any{"repository": repository},
	}); err != nil {
		return err
	}
	return commit(tx, "plugin GitHub token replacement")
}

func (store *Store) ListPluginVersions(ctx context.Context, pluginID string) ([]PluginVersion, error) {
	rows, err := store.db.QueryContext(ctx, pluginVersionSelect+`
WHERE plugin_id = ? ORDER BY installed_at DESC, version`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list plugin versions: %w", err)
	}
	defer rows.Close()
	values := make([]PluginVersion, 0)
	for rows.Next() {
		value, err := scanPluginVersion(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plugin versions: %w", err)
	}
	return values, nil
}

func (store *Store) RecordPluginRuntimeStatus(ctx context.Context, pluginID, state, health string,
	restartCount uint64, problem *protocol.Problem, observedAt time.Time,
) error {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return err
	}
	switch state {
	case "active", "failed":
	default:
		return fmt.Errorf("unsupported plugin state %q", state)
	}
	switch health {
	case "healthy", "unhealthy", "unknown":
	default:
		return fmt.Errorf("unsupported plugin health %q", health)
	}
	var problemJSON any
	if problem != nil {
		if err := problem.Validate(); err != nil {
			return fmt.Errorf("validate plugin problem: %w", err)
		}
		raw, err := json.Marshal(problem)
		if err != nil {
			return fmt.Errorf("encode plugin problem: %w", err)
		}
		problemJSON = string(raw)
	}
	result, err := store.db.ExecContext(ctx, `
UPDATE plugin_installations SET
    state = ?, health = ?, restart_count = ?, last_problem_json = ?,
    last_started_at = CASE WHEN ? = 'healthy' THEN ? ELSE last_started_at END,
    updated_at = ?
WHERE plugin_id = ?`, state, health, int64(restartCount), problemJSON,
		health, unixTime(observedAt), unixTime(observedAt), pluginID)
	if err != nil {
		return fmt.Errorf("record plugin runtime status: %w", err)
	}
	return expectOneAgentUpdate(result, "plugin runtime status")
}

func (store *Store) DeletePluginInstallation(ctx context.Context, pluginID string, now time.Time) ([]PluginVersion, error) {
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return nil, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin plugin uninstall: %w", err)
	}
	defer tx.Rollback()
	var dependent int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM node_plugin_instances
WHERE plugin_id = ? AND (desired_state <> 'absent' OR actual_state <> 'absent')`, pluginID).Scan(&dependent); err != nil {
		return nil, fmt.Errorf("check node plugin instances before uninstall: %w", err)
	}
	if dependent != 0 {
		return nil, ErrStateConflict
	}
	versions, err := listPluginVersionsTx(ctx, tx, pluginID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM service_bindings WHERE plugin_id = ?", pluginID); err != nil {
		return nil, fmt.Errorf("delete plugin service bindings: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM plugin_installations WHERE plugin_id = ?`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("delete plugin installation: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read plugin uninstall result: %w", err)
	}
	if deleted != 1 {
		return nil, ErrNotFound
	}
	if err := appendAuditTx(ctx, tx, AuditEntry{
		OccurredAt: now, ActorType: "administrator", ActorID: "1", Action: "plugin.uninstall",
		TargetType: "plugin_installation", TargetID: pluginID, Outcome: "success",
	}); err != nil {
		return nil, err
	}
	if err := commit(tx, "plugin uninstall"); err != nil {
		return nil, err
	}
	return versions, nil
}

func validateCommittedPlugin(installation PluginInstallation, version PluginVersion) error {
	if err := manifest.Validate(version.Manifest); err != nil {
		return fmt.Errorf("validate plugin manifest: %w", err)
	}
	if err := validatePluginRepository(installation.Repository); err != nil {
		return err
	}
	if installation.PluginID != version.PluginID || installation.PluginID != version.Manifest.ID ||
		installation.Kind != string(version.Manifest.Kind) || installation.ActiveVersion != version.Version ||
		installation.DesiredVersion != version.Version || version.Version != version.Manifest.Version {
		return errors.New("plugin release metadata does not match the installation")
	}
	if version.ReleaseID < 1 || version.ReleaseTag != "v"+version.Version || version.CenterAssetID < 1 {
		return errors.New("plugin release identifiers are invalid")
	}
	if err := validateApprovedPermissions(version.Manifest, version.ApprovedPermissions); err != nil {
		return err
	}
	if !sort.StringsAreSorted(version.ApprovedPermissions) {
		return errors.New("approved plugin permissions must be sorted")
	}
	var hasNode, hasUI bool
	for _, artifact := range version.Manifest.Artifacts {
		switch artifact.Role {
		case manifest.ArtifactNode:
			hasNode = true
		case manifest.ArtifactUI:
			hasUI = true
		}
	}
	if hasNode != (version.NodeAssetID != nil) || hasUI != (version.UIAssetID != nil) {
		return errors.New("plugin release asset identifiers do not match the manifest")
	}
	return nil
}

const pluginVersionSelect = `
SELECT plugin_id, version, release_id, release_tag, manifest_json, approved_permissions_json,
       center_asset_id, node_asset_id, ui_asset_id, installed_at
FROM plugin_versions `

func scanPluginVersion(row rowScanner) (PluginVersion, error) {
	var value PluginVersion
	var manifestJSON, approvedJSON []byte
	var nodeAssetID, uiAssetID sql.NullInt64
	var installedAt int64
	if err := row.Scan(
		&value.PluginID, &value.Version, &value.ReleaseID, &value.ReleaseTag, &manifestJSON, &approvedJSON,
		&value.CenterAssetID, &nodeAssetID, &uiAssetID, &installedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PluginVersion{}, ErrNotFound
		}
		return PluginVersion{}, fmt.Errorf("scan plugin version: %w", err)
	}
	decoded, err := manifest.Decode(bytes.NewReader(manifestJSON))
	if err != nil {
		return PluginVersion{}, fmt.Errorf("decode plugin version manifest: %w", err)
	}
	if err := json.Unmarshal(approvedJSON, &value.ApprovedPermissions); err != nil {
		return PluginVersion{}, fmt.Errorf("decode plugin version permissions: %w", err)
	}
	value.Manifest = decoded
	value.NodeAssetID = nullableInt64(nodeAssetID)
	value.UIAssetID = nullableInt64(uiAssetID)
	value.InstalledAt = fromUnix(installedAt)
	return value, nil
}

func pluginVersionByIDTx(ctx context.Context, tx *sql.Tx, pluginID, version string) (PluginVersion, error) {
	return scanPluginVersion(tx.QueryRowContext(ctx, pluginVersionSelect+`
WHERE plugin_id = ? AND version = ?`, pluginID, version))
}

func listPluginVersionsTx(ctx context.Context, tx *sql.Tx, pluginID string) ([]PluginVersion, error) {
	rows, err := tx.QueryContext(ctx, pluginVersionSelect+` WHERE plugin_id = ? ORDER BY installed_at DESC, version`, pluginID)
	if err != nil {
		return nil, fmt.Errorf("list plugin versions: %w", err)
	}
	defer rows.Close()
	values := make([]PluginVersion, 0)
	for rows.Next() {
		value, err := scanPluginVersion(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plugin versions: %w", err)
	}
	return values, nil
}

func samePluginVersion(left, right PluginVersion) bool {
	if left.PluginID != right.PluginID || left.Version != right.Version || left.ReleaseID != right.ReleaseID ||
		left.ReleaseTag != right.ReleaseTag || left.CenterAssetID != right.CenterAssetID ||
		!equalOptionalInt64(left.NodeAssetID, right.NodeAssetID) || !equalOptionalInt64(left.UIAssetID, right.UIAssetID) ||
		!equalStrings(left.ApprovedPermissions, right.ApprovedPermissions) {
		return false
	}
	leftJSON, leftErr := json.Marshal(left.Manifest)
	rightJSON, rightErr := json.Marshal(right.Manifest)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func equalOptionalInt64(left, right *int64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func equalStrings(left, right []string) bool {
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}
