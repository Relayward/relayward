package pluginruntime

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/store"
)

type hostService struct {
	centerpluginv1.UnimplementedPluginHostServer
	database    database
	events      eventPublisher
	pluginID    string
	permissions map[string]struct{}
	now         func() time.Time
}

func newHostService(database database, events eventPublisher, pluginID string, permissions []string) *hostService {
	approved := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		approved[permission] = struct{}{}
	}
	return &hostService{database: database, events: events, pluginID: pluginID, permissions: approved, now: func() time.Time { return time.Now().UTC() }}
}

func (service *hostService) PublishEvents(ctx context.Context, request *centerpluginv1.PublishEventsRequest) (*centerpluginv1.EventsPublished, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionEventsWrite]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.events.write permission is required")
	}
	if err := centerpluginv1.ValidatePublishEventsRequest(request, service.pluginID); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid published event batch")
	}
	nodes, err := service.database.ListNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "node state is unavailable")
	}
	knownNodes := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		knownNodes[node.ID] = struct{}{}
	}
	for _, event := range request.Events {
		if _, exists := knownNodes[event.NodeId]; !exists {
			return nil, status.Error(codes.FailedPrecondition, "published event references an unknown node")
		}
	}
	if err := service.events.PublishPluginEvents(ctx, service.pluginID, request, service.now()); err != nil {
		if errors.Is(err, eventstore.ErrConflict) {
			return nil, status.Error(codes.AlreadyExists, "published event ID conflicts with existing content")
		}
		return nil, status.Error(codes.Internal, "published events are unavailable")
	}
	response := &centerpluginv1.EventsPublished{EventCount: uint32(len(request.Events))}
	if err := centerpluginv1.ValidateEventsPublished(request, service.pluginID, response); err != nil {
		return nil, status.Error(codes.Internal, "published event result is invalid")
	}
	return response, nil
}

func (service *hostService) ReplaceServices(ctx context.Context, request *centerpluginv1.ReplaceServicesRequest) (*centerpluginv1.ServicesReplaced, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionServicesWrite]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.services.write permission is required")
	}
	if err := centerpluginv1.ValidateReplaceServicesRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid plugin service catalog")
	}
	services := make([]store.PluginService, len(request.Services))
	for index, value := range request.Services {
		services[index] = store.PluginService{
			NodeID: request.NodeId, PluginID: service.pluginID, ServiceID: value.Id,
			DisplayName: value.DisplayName, Enabled: value.Enabled,
			Capabilities: append([]string(nil), value.Capabilities...), SubscriptionSHA256: value.SubscriptionSha256,
		}
	}
	if err := service.database.ReplacePluginServices(ctx, service.pluginID, request.NodeId, services, service.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "plugin is not installed on the node")
		}
		return nil, status.Error(codes.Internal, "plugin service catalog is unavailable")
	}
	response := &centerpluginv1.ServicesReplaced{ServiceCount: uint32(len(services))}
	if err := centerpluginv1.ValidateServicesReplaced(request, response); err != nil {
		return nil, status.Error(codes.Internal, "plugin service catalog result is invalid")
	}
	return response, nil
}

func (service *hostService) ListNodes(ctx context.Context, _ *centerpluginv1.ListNodesRequest) (*centerpluginv1.ListNodesResponse, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionNodesRead]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.nodes.read permission is required")
	}
	nodes, err := service.database.ListNodes(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "node state is unavailable")
	}
	response := &centerpluginv1.ListNodesResponse{Nodes: make([]*centerpluginv1.Node, len(nodes))}
	now := service.now()
	for index, node := range nodes {
		connected := node.Enabled && node.LastSeenAt != nil && now.Sub(*node.LastSeenAt) <= 3*agentv1.DefaultHeartbeatInterval
		response.Nodes[index] = &centerpluginv1.Node{Id: node.ID, Name: node.Name, Enabled: node.Enabled, Connected: connected}
	}
	if err := centerpluginv1.ValidateListNodesResponse(response); err != nil {
		return nil, status.Error(codes.Internal, "node state is invalid")
	}
	return response, nil
}

func permissionDenied(err error) bool {
	return errors.Is(err, status.Error(codes.PermissionDenied, "")) || status.Code(err) == codes.PermissionDenied
}
