package pluginruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/store"
)

func TestSupervisorRunsRealPluginHostUIAndRestoresPreviousVersion(t *testing.T) {
	root := shortRuntimeRoot(t)
	database, err := store.Open(t.Context(), filepath.Join(root, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	artifacts, err := pluginartifact.Open(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	nodeID := "10000000-0000-4000-8000-000000000001"
	if err := database.CreateNode(t.Context(), store.Node{ID: nodeID, Name: "Edge", Enabled: true}, now); err != nil {
		t.Fatal(err)
	}
	version := installTestExecutable(t, artifacts, "1.2.3")
	installation := store.PluginInstallation{
		PluginID: version.PluginID, Repository: "https://github.com/Relayward/contract-plugin",
		Kind: string(version.Manifest.Kind), DesiredVersion: version.Version,
		ActiveVersion: version.Version, Manifest: version.Manifest,
	}
	if _, err := database.CommitPluginRelease(t.Context(), installation, version, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordPluginRuntimeStatus(t.Context(), version.PluginID, "failed", "unhealthy", 2, nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	supervisor, err := New(database, artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	supervisor.healthInterval = 20 * time.Millisecond
	t.Setenv("RELAYWARD_TEST_SECRET", "must-not-be-inherited")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := supervisor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	raw, err := supervisor.InvokeUI(t.Context(), version.PluginID, "nodes.summary", []byte(`{}`))
	if err != nil || string(raw) != `{"count":1}` {
		t.Fatalf("InvokeUI() = %s, %v", raw, err)
	}
	consumerIDs, err := supervisor.FeatureConsumerIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(consumerIDs) != 1 || consumerIDs[0] != featureConsumerID(version.PluginID) {
		t.Fatalf("FeatureConsumerIDs() = %v", consumerIDs)
	}
	event, err := agentv1.NewEvent(nodeID, "0123456789abcdef0123456789abcdef", 1, "system.test", now, map[string]string{"state": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.ConsumeFeatureEvents(t.Context(), consumerIDs[0], []eventstore.StoredEvent{{
		RowID: 1, NodeID: nodeID, Event: event, ReceivedAt: now.Add(time.Second),
	}}); err != nil {
		t.Fatalf("ConsumeFeatureEvents() error = %v", err)
	}
	recovered, err := database.PluginInstallationByID(t.Context(), version.PluginID)
	if err != nil || recovered.State != "active" || recovered.Health != "healthy" || recovered.RestartCount != 2 {
		t.Fatalf("recovered installation = %+v, %v", recovered, err)
	}

	incompatible := installTestExecutable(t, artifacts, "1.2.4")
	switchContext, switchCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer switchCancel()
	if err := supervisor.Switch(switchContext, incompatible); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("Switch() identity mismatch error = %v", err)
	}
	raw, err = supervisor.InvokeUI(t.Context(), version.PluginID, "nodes.summary", []byte(`{}`))
	if err != nil || string(raw) != `{"count":1}` {
		t.Fatalf("InvokeUI() after rollback = %s, %v", raw, err)
	}
	closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	if err := supervisor.Close(closeContext); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSupervisorRetriesPreviousVersionAfterImmediateRestoreFailure(t *testing.T) {
	root := shortRuntimeRoot(t)
	database, err := store.Open(t.Context(), filepath.Join(root, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	artifacts, err := pluginartifact.Open(filepath.Join(root, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	previous := installTestExecutable(t, artifacts, "1.2.3")
	installation := store.PluginInstallation{
		PluginID: previous.PluginID, Repository: "https://github.com/Relayward/contract-plugin",
		Kind: string(previous.Manifest.Kind), DesiredVersion: previous.Version,
		ActiveVersion: previous.Version, Manifest: previous.Manifest,
	}
	if _, err := database.CommitPluginRelease(t.Context(), installation, previous, nil, now); err != nil {
		t.Fatal(err)
	}
	supervisor, err := New(database, artifacts, nil)
	if err != nil {
		t.Fatal(err)
	}
	supervisor.healthInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := supervisor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeContext, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		_ = supervisor.Close(closeContext)
	}()

	previousPaths, err := artifacts.Paths(previous.PluginID, previous.Version)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(previousPaths.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(previousPaths.Executable, previousPaths.Executable+".running"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previousPaths.Executable, []byte("broken"), 0o700); err != nil {
		t.Fatal(err)
	}
	incompatible := installTestExecutable(t, artifacts, "1.2.4")
	switchContext, switchCancel := context.WithTimeout(t.Context(), 10*time.Second)
	err = supervisor.Switch(switchContext, incompatible)
	switchCancel()
	if err == nil || !strings.Contains(err.Error(), "restore previous center plugin") {
		t.Fatalf("Switch() restore error = %v", err)
	}
	if err := os.Remove(previousPaths.Executable); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previousPaths.Executable, original, 0o700); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, invokeErr := supervisor.InvokeUI(t.Context(), previous.PluginID, "nodes.summary", []byte(`{}`))
		if invokeErr == nil && string(raw) == `{"count":0}` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("previous plugin did not recover: response = %s, error = %v", raw, invokeErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestHostRequiresDeclaredPermission(t *testing.T) {
	root := shortRuntimeRoot(t)
	database, _ := store.Open(t.Context(), filepath.Join(root, "relayward.db"))
	defer database.Close()
	host := newHostService(database, "io.relayward.test", nil)
	if _, err := host.ListNodes(t.Context(), &centerpluginv1.ListNodesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("ListNodes() permission error = %v", err)
	}
}

func TestFeatureConsumersRequireFeatureKindAndPermission(t *testing.T) {
	feature := &pluginActor{version: &store.PluginVersion{
		PluginID: "io.relayward.feature", Version: "2.0.0", Manifest: manifest.Manifest{Kind: manifest.KindFeature},
		ApprovedPermissions: []string{centerpluginv1.PermissionEventsRead},
	}}
	runtimePlugin := &pluginActor{version: &store.PluginVersion{
		PluginID: "io.relayward.runtime", Manifest: manifest.Manifest{Kind: manifest.KindRuntime},
		ApprovedPermissions: []string{centerpluginv1.PermissionEventsRead},
	}}
	missingPermission := &pluginActor{version: &store.PluginVersion{
		PluginID: "io.relayward.no-events", Manifest: manifest.Manifest{Kind: manifest.KindFeature},
	}}
	database := &hostDatabaseStub{installations: []store.PluginInstallation{
		{PluginID: "io.relayward.feature", Kind: string(manifest.KindFeature), ActiveVersion: "1.0.0", State: "active",
			Manifest: manifest.Manifest{Kind: manifest.KindFeature, Version: "1.0.0"}, ApprovedPermissions: []string{centerpluginv1.PermissionEventsRead}},
		{PluginID: "io.relayward.runtime", Kind: string(manifest.KindRuntime), ActiveVersion: "1.0.0", State: "active",
			Manifest: manifest.Manifest{Kind: manifest.KindRuntime, Version: "1.0.0"}, ApprovedPermissions: []string{centerpluginv1.PermissionEventsRead}},
		{PluginID: "io.relayward.no-events", Kind: string(manifest.KindFeature), ActiveVersion: "1.0.0", State: "active",
			Manifest: manifest.Manifest{Kind: manifest.KindFeature, Version: "1.0.0"}},
	}}
	supervisor := &Supervisor{database: database, actors: map[string]*pluginActor{
		"io.relayward.feature": feature, "io.relayward.runtime": runtimePlugin,
		"io.relayward.no-events": missingPermission,
	}}
	consumerIDs, err := supervisor.FeatureConsumerIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(consumerIDs) != 1 || consumerIDs[0] != featureConsumerID("io.relayward.feature") {
		t.Fatalf("FeatureConsumerIDs() = %v", consumerIDs)
	}
	if err := supervisor.ConsumeFeatureEvents(t.Context(), consumerIDs[0], nil); err == nil {
		t.Fatal("ConsumeFeatureEvents() accepted an uncommitted process version")
	}
	if _, err := featurePluginID("standard.access.v1"); err == nil {
		t.Fatal("featurePluginID() accepted a standard consumer ID")
	}
}

type hostDatabaseStub struct {
	pluginID      string
	nodeID        string
	services      []store.PluginService
	installations []store.PluginInstallation
}

func (database *hostDatabaseStub) ListPluginInstallations(context.Context) ([]store.PluginInstallation, error) {
	return append([]store.PluginInstallation(nil), database.installations...), nil
}
func (database *hostDatabaseStub) PluginInstallationByID(_ context.Context, pluginID string) (store.PluginInstallation, error) {
	for _, installation := range database.installations {
		if installation.PluginID == pluginID {
			return installation, nil
		}
	}
	return store.PluginInstallation{}, store.ErrNotFound
}
func (*hostDatabaseStub) PluginVersionByID(context.Context, string, string) (store.PluginVersion, error) {
	return store.PluginVersion{}, store.ErrNotFound
}
func (*hostDatabaseStub) ListNodes(context.Context) ([]store.Node, error) { return nil, nil }
func (database *hostDatabaseStub) ReplacePluginServices(_ context.Context, pluginID, nodeID string,
	services []store.PluginService, _ time.Time,
) error {
	database.pluginID = pluginID
	database.nodeID = nodeID
	database.services = append([]store.PluginService(nil), services...)
	return nil
}
func (*hostDatabaseStub) RecordPluginRuntimeStatus(context.Context, string, string, string, uint64, *protocol.Problem, time.Time) error {
	return nil
}

func TestHostBindsPluginIdentityWhenReplacingServices(t *testing.T) {
	database := &hostDatabaseStub{}
	host := newHostService(database, "io.relayward.expected", []string{centerpluginv1.PermissionServicesWrite})
	request := &centerpluginv1.ReplaceServicesRequest{
		NodeId: "10000000-0000-4000-8000-000000000001",
		Services: []*centerpluginv1.PluginService{{
			Id: "main", DisplayName: "Main", Enabled: true,
			Capabilities: []string{"subscription.render"}, SubscriptionSha256: strings.Repeat("a", 64),
		}},
	}
	response, err := host.ReplaceServices(t.Context(), request)
	if err != nil || response.ServiceCount != 1 {
		t.Fatalf("ReplaceServices() = %+v, %v", response, err)
	}
	if database.pluginID != "io.relayward.expected" || database.nodeID != request.NodeId ||
		len(database.services) != 1 || database.services[0].PluginID != "io.relayward.expected" {
		t.Fatalf("captured service replacement = %+v", database)
	}
}

func installTestExecutable(t *testing.T, artifacts *pluginartifact.Store, version string) store.PluginVersion {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	paths, err := artifacts.Paths("io.relayward.contract-test", version)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Executable, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	pluginManifest := manifest.Manifest{
		APIVersion: "relayward.plugin/v1", ID: "io.relayward.contract-test", Name: "Contract test",
		Version: version, Kind: manifest.KindFeature, Requires: manifest.Requirements{ControlAPI: 1},
		Permissions: []manifest.Permission{
			{Name: centerpluginv1.PermissionEventsRead, Reason: "Exercise event consumption."},
			{Name: centerpluginv1.PermissionNodesRead, Reason: "Exercise node access."},
		},
		Artifacts: []manifest.Artifact{{
			Role: manifest.ArtifactCenter, File: "center", Size: int64(len(raw)),
			SHA256: hex.EncodeToString(digest[:]), OS: "linux", Arch: "amd64",
		}},
	}
	return store.PluginVersion{
		PluginID: pluginManifest.ID, Version: version, ReleaseID: 10, ReleaseTag: "v" + version,
		Manifest:            pluginManifest,
		ApprovedPermissions: []string{centerpluginv1.PermissionEventsRead, centerpluginv1.PermissionNodesRead}, CenterAssetID: 2,
	}
}

func shortRuntimeRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "rwc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
