package pluginruntime

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
)

type hostService struct {
	centerpluginv1.UnimplementedPluginHostServer
	database    database
	permissions map[string]struct{}
	now         func() time.Time
}

func newHostService(database database, permissions []string) *hostService {
	approved := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		approved[permission] = struct{}{}
	}
	return &hostService{database: database, permissions: approved, now: func() time.Time { return time.Now().UTC() }}
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
