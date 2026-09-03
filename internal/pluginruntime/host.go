package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/networkdiagnostics"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

type hostService struct {
	centerpluginv1.UnimplementedPluginHostServer
	database    database
	events      eventPublisher
	nodePlugins nodePluginManager
	diagnostics portDiagnoser
	pluginID    string
	version     string
	permissions map[string]struct{}
	now         func() time.Time
}

func newHostService(database database, events eventPublisher, nodePlugins nodePluginManager, diagnostics portDiagnoser,
	pluginID, version string, permissions []string,
) *hostService {
	approved := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		approved[permission] = struct{}{}
	}
	return &hostService{
		database: database, events: events, nodePlugins: nodePlugins, diagnostics: diagnostics, pluginID: pluginID, version: version,
		permissions: approved, now: func() time.Time { return time.Now().UTC() },
	}
}

func (service *hostService) DiagnoseNodePorts(ctx context.Context,
	request *centerpluginv1.DiagnoseNodePortsRequest,
) (*centerpluginv1.DiagnoseNodePortsResponse, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionPortDiagnose]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.network_diagnostics.read permission is required")
	}
	if err := centerpluginv1.ValidateDiagnoseNodePortsRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid node port diagnostic request")
	}
	if service.diagnostics == nil {
		return nil, status.Error(codes.Unavailable, "network diagnostics are unavailable")
	}
	response, err := service.diagnostics.Diagnose(ctx, service.pluginID, request)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound), errors.Is(err, networkdiagnostics.ErrUnknownService):
			return nil, status.Error(codes.FailedPrecondition, "network diagnostic references unavailable node state")
		case errors.Is(err, networkdiagnostics.ErrTooManyProbes):
			return nil, status.Error(codes.ResourceExhausted, "network diagnostic contains too many public probes")
		default:
			return nil, status.Error(codes.Internal, "network diagnostics are unavailable")
		}
	}
	if err := centerpluginv1.ValidateDiagnoseNodePortsResponse(request, response); err != nil {
		return nil, status.Error(codes.Internal, "network diagnostic result is invalid")
	}
	return response, nil
}

func (service *hostService) ListNodeAuthorizations(ctx context.Context,
	request *centerpluginv1.ListNodeAuthorizationsRequest,
) (*centerpluginv1.ListNodeAuthorizationsResponse, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionAuthorizationsRead]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.authorizations.read permission is required")
	}
	if err := centerpluginv1.ValidateListNodeAuthorizationsRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid node authorization request")
	}
	values, err := service.database.ListPluginNodeAuthorizations(ctx, request.NodeId, service.pluginID)
	if err != nil {
		return nil, status.Error(codes.Internal, "node authorizations are unavailable")
	}
	response := &centerpluginv1.ListNodeAuthorizationsResponse{Authorizations: make([]*centerpluginv1.NodeAuthorization, len(values))}
	for index, value := range values {
		response.Authorizations[index] = &centerpluginv1.NodeAuthorization{
			Id: value.ID, UserIdentifier: value.UserIdentifier, Enabled: value.Enabled,
			ServiceIds: append([]string(nil), value.ServiceIDs...),
		}
	}
	if err := centerpluginv1.ValidateListNodeAuthorizationsResponse(request, response); err != nil {
		return nil, status.Error(codes.Internal, "stored node authorizations are invalid")
	}
	return response, nil
}

func (service *hostService) DiagnoseNodePlugin(ctx context.Context,
	request *centerpluginv1.DiagnoseNodePluginRequest,
) (*centerpluginv1.DiagnoseNodePluginResponse, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionNodeDiagnose]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.node_plugins.diagnose permission is required")
	}
	if err := centerpluginv1.ValidateDiagnoseNodePluginRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid node plugin diagnostic request")
	}
	if service.nodePlugins == nil {
		return nil, status.Error(codes.Unavailable, "node plugin diagnostics are unavailable")
	}
	raw, err := service.nodePlugins.DiagnoseNodePlugin(ctx, request.NodeId, service.pluginID, request.Name, append(json.RawMessage(nil), request.Json...))
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, "node plugin diagnostic was canceled")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(codes.DeadlineExceeded, "node plugin diagnostic timed out")
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrStateConflict):
			return nil, status.Error(codes.FailedPrecondition, "node plugin is unavailable")
		default:
			return nil, status.Error(codes.Internal, "node plugin diagnostic failed")
		}
	}
	response := &centerpluginv1.DiagnoseNodePluginResponse{Json: append([]byte(nil), raw...)}
	if err := centerpluginv1.ValidateDiagnoseNodePluginResponse(request, response); err != nil {
		return nil, status.Error(codes.Internal, "node plugin diagnostic result is invalid")
	}
	return response, nil
}

func (service *hostService) GetNodePluginConfiguration(ctx context.Context,
	request *centerpluginv1.GetNodePluginConfigurationRequest,
) (*centerpluginv1.NodePluginConfiguration, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionNodeConfigure]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.node_plugins.configure permission is required")
	}
	if err := centerpluginv1.ValidateGetNodePluginConfigurationRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid node plugin configuration request")
	}
	instance, configuration, err := service.nodePlugins.NodePluginConfiguration(ctx, request.NodeId, service.pluginID)
	if err != nil {
		return nil, nodePluginConfigurationError(err, true)
	}
	response := &centerpluginv1.NodePluginConfiguration{
		Generation: instance.Generation,
		Version:    instance.DesiredVersion,
		Sha256:     instance.DesiredConfigurationSHA256,
		Json:       append([]byte(nil), configuration...),
	}
	if err := centerpluginv1.ValidateNodePluginConfiguration(request, response); err != nil {
		return nil, status.Error(codes.Internal, "stored node plugin configuration is invalid")
	}
	return response, nil
}

func (service *hostService) ConfigureNodePlugin(ctx context.Context,
	request *centerpluginv1.ConfigureNodePluginRequest,
) (*centerpluginv1.NodePluginConfigured, error) {
	if _, approved := service.permissions[centerpluginv1.PermissionNodeConfigure]; !approved {
		return nil, status.Error(codes.PermissionDenied, "core.node_plugins.configure permission is required")
	}
	if err := centerpluginv1.ValidateConfigureNodePluginRequest(request); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid node plugin configuration")
	}
	instance, err := service.nodePlugins.ConfigureNodePlugin(
		ctx, request.NodeId, service.pluginID, service.version, request.ExpectedGeneration,
		append(json.RawMessage(nil), request.Json...),
	)
	if err != nil {
		return nil, nodePluginConfigurationError(err, false)
	}
	response := &centerpluginv1.NodePluginConfigured{
		Generation: instance.Generation,
		Sha256:     instance.DesiredConfigurationSHA256,
	}
	if err := centerpluginv1.ValidateNodePluginConfigured(request, response); err != nil {
		return nil, status.Error(codes.Internal, "configured node plugin state is invalid")
	}
	return response, nil
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

func nodePluginConfigurationError(err error, reading bool) error {
	switch {
	case errors.Is(err, secretbox.ErrUnavailable):
		return status.Error(codes.Unavailable, "encrypted node plugin configuration is unavailable")
	case errors.Is(err, store.ErrGenerationConflict):
		return status.Error(codes.Aborted, "node plugin configuration generation changed")
	case errors.Is(err, store.ErrNotFound) && reading:
		return status.Error(codes.NotFound, "node plugin configuration does not exist")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrStateConflict):
		return status.Error(codes.FailedPrecondition, "node plugin cannot be configured in its current state")
	default:
		return status.Error(codes.Internal, "node plugin configuration is unavailable")
	}
}
