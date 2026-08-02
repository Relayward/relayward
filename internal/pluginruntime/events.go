package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/contract"
	"github.com/Relayward/relayward-sdk/manifest"
	"google.golang.org/grpc/status"

	"github.com/Relayward/relayward/internal/eventstore"
)

const (
	featureConsumerPrefix = "feature."
	featureConsumerSuffix = ".events.v1"
)

func (supervisor *Supervisor) FeatureConsumerIDs(ctx context.Context) ([]string, error) {
	installations, err := supervisor.database.ListPluginInstallations(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(installations))
	for _, installation := range installations {
		if installation.ActiveVersion == "" ||
			(installation.State != "active" && installation.State != "failed") ||
			installation.Kind != string(manifest.KindFeature) || installation.Manifest.Kind != manifest.KindFeature ||
			!hasApprovedPermission(installation.ApprovedPermissions, centerpluginv1.PermissionEventsRead) {
			continue
		}
		values = append(values, featureConsumerID(installation.PluginID))
	}
	sort.Strings(values)
	return values, nil
}

func (supervisor *Supervisor) ConsumeFeatureEvents(ctx context.Context, consumerID string, sources []eventstore.StoredEvent) error {
	pluginID, err := featurePluginID(consumerID)
	if err != nil {
		return err
	}
	supervisor.mu.Lock()
	actor := supervisor.actors[pluginID]
	supervisor.mu.Unlock()
	if actor == nil {
		return ErrPluginUnavailable
	}
	actor.mu.Lock()
	version := cloneVersion(actor.version)
	process := actor.process
	actor.mu.Unlock()
	installation, err := supervisor.database.PluginInstallationByID(ctx, pluginID)
	if err != nil {
		return ErrPluginUnavailable
	}
	if version == nil || version.Manifest.Kind != manifest.KindFeature ||
		installation.ActiveVersion != version.Version || installation.Manifest.Version != version.Version ||
		installation.Kind != string(manifest.KindFeature) || installation.Manifest.Kind != manifest.KindFeature ||
		!hasApprovedPermission(version.ApprovedPermissions, centerpluginv1.PermissionEventsRead) ||
		!hasApprovedPermission(installation.ApprovedPermissions, centerpluginv1.PermissionEventsRead) {
		return errors.New("feature plugin event permission is unavailable")
	}
	if process == nil || process.exited() {
		return ErrPluginUnavailable
	}
	request := &centerpluginv1.ConsumeEventsRequest{Events: make([]*centerpluginv1.StandardEvent, len(sources))}
	for index, source := range sources {
		if source.RowID < 1 {
			return errors.New("feature event cursor is invalid")
		}
		request.Events[index] = &centerpluginv1.StandardEvent{
			Cursor: uint64(source.RowID), EventId: source.Event.EventID, NodeId: source.NodeID,
			Kind: source.Event.Kind, ObservedAtUnixNano: source.Event.ObservedAt.UTC().UnixNano(),
			ReceivedAtUnixNano: source.ReceivedAt.UTC().UnixNano(), Json: append([]byte(nil), source.Event.Payload...),
		}
	}
	if err := centerpluginv1.ValidateConsumeEventsRequest(request); err != nil {
		return fmt.Errorf("build feature event batch: %w", err)
	}
	callContext, cancel := context.WithTimeout(ctx, pluginRPCTimeout)
	defer cancel()
	response, err := process.client.ConsumeEvents(callContext, request)
	if err != nil {
		return fmt.Errorf("feature plugin event RPC failed with %s", status.Code(err))
	}
	if err := centerpluginv1.ValidateEventsConsumed(request, response); err != nil {
		return errors.New("feature plugin returned an invalid event acknowledgement")
	}
	return nil
}

func featureConsumerID(pluginID string) string {
	return featureConsumerPrefix + pluginID + featureConsumerSuffix
}

func featurePluginID(consumerID string) (string, error) {
	if !strings.HasPrefix(consumerID, featureConsumerPrefix) || !strings.HasSuffix(consumerID, featureConsumerSuffix) {
		return "", errors.New("invalid feature consumer ID")
	}
	pluginID := strings.TrimSuffix(strings.TrimPrefix(consumerID, featureConsumerPrefix), featureConsumerSuffix)
	if err := contract.ValidatePluginID(pluginID); err != nil {
		return "", errors.New("invalid feature consumer ID")
	}
	return pluginID, nil
}

func hasApprovedPermission(permissions []string, permission string) bool {
	index := sort.SearchStrings(permissions, permission)
	return index < len(permissions) && permissions[index] == permission
}
