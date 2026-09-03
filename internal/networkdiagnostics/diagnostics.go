package networkdiagnostics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync"
	"syscall"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"

	"github.com/Relayward/relayward/internal/store"
)

const (
	maximumTCPProbes = 96
	probeConcurrency = 16
	probeTimeout     = 2 * time.Second
)

var (
	ErrUnknownService = errors.New("network diagnostic references an unknown plugin service")
	ErrTooManyProbes  = errors.New("network diagnostic requires too many public probes")
)

type database interface {
	ListNodeEndpoints(context.Context, string) ([]store.NodeEndpoint, error)
	ListNodePublicAddresses(context.Context, string) ([]store.NodePublicAddress, error)
	ListPluginServices(context.Context, string) ([]store.PluginService, error)
}

type nodeState struct {
	sessionID string
	listeners []agentv1.PluginListenerStatus
}

type Service struct {
	database database

	mu    sync.RWMutex
	nodes map[string]nodeState

	lookup func(context.Context, string) ([]netip.Addr, error)
	dial   func(context.Context, string) error
}

func New(database database) (*Service, error) {
	if database == nil {
		return nil, errors.New("network diagnostic database is required")
	}
	dialer := &net.Dialer{}
	return &Service{
		database: database,
		nodes:    make(map[string]nodeState),
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dial: func(ctx context.Context, address string) error {
			connection, err := dialer.DialContext(ctx, "tcp", address)
			if err != nil {
				return err
			}
			return connection.Close()
		},
	}, nil
}

func (service *Service) Activate(nodeID, sessionID string) {
	service.mu.Lock()
	service.nodes[nodeID] = nodeState{sessionID: sessionID}
	service.mu.Unlock()
}

func (service *Service) Update(nodeID, sessionID string, listeners []agentv1.PluginListenerStatus) {
	service.mu.Lock()
	defer service.mu.Unlock()
	current, exists := service.nodes[nodeID]
	if !exists || current.sessionID != sessionID {
		return
	}
	current.listeners = append([]agentv1.PluginListenerStatus(nil), listeners...)
	service.nodes[nodeID] = current
}

func (service *Service) Deactivate(nodeID, sessionID string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if current, exists := service.nodes[nodeID]; exists && current.sessionID == sessionID {
		delete(service.nodes, nodeID)
	}
}

func (service *Service) Diagnose(ctx context.Context, pluginID string,
	request *centerpluginv1.DiagnoseNodePortsRequest,
) (*centerpluginv1.DiagnoseNodePortsResponse, error) {
	if err := service.requirePluginServices(ctx, request.NodeId, pluginID, request.Ports); err != nil {
		return nil, err
	}
	endpoints, err := service.resolvedEndpoints(ctx, request.NodeId)
	if err != nil {
		return nil, err
	}
	state, connected := service.snapshot(request.NodeId)
	response := &centerpluginv1.DiagnoseNodePortsResponse{
		Diagnostics: make([]*centerpluginv1.ServicePortDiagnostic, len(request.Ports)),
	}
	probes := make([]publicProbe, 0)
	for index, port := range request.Ports {
		diagnostic := &centerpluginv1.ServicePortDiagnostic{
			ServiceId: port.ServiceId, Network: port.Network, LocalPort: port.Port,
			LocalState: centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_UNKNOWN,
			Endpoints:  make([]*centerpluginv1.EndpointPortDiagnostic, len(endpoints)),
		}
		if connected {
			applyLocalListener(diagnostic, state.listeners, pluginID)
		}
		for endpointIndex, endpoint := range endpoints {
			publicPort := port.Port
			if overrides := endpoint.value.PublicPortOverrides[pluginID]; overrides != nil {
				if override, exists := overrides[port.ServiceId]; exists {
					publicPort = uint32(override)
				}
			}
			result := &centerpluginv1.EndpointPortDiagnostic{
				EndpointId: endpoint.value.ID, DisplayName: endpoint.value.DisplayName,
				Kind: endpoint.value.Kind, Address: endpoint.address, Port: publicPort,
			}
			diagnostic.Endpoints[endpointIndex] = result
			switch {
			case !connected:
				markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_NODE_OFFLINE)
			case diagnostic.LocalState == centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_NOT_LISTENING:
				markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_LOCAL_NOT_LISTENING)
			case !endpoint.available:
				markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_ENDPOINT_UNAVAILABLE)
			case endpoint.value.Proxied:
				markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_PROXIED_ENDPOINT)
			case port.Network != "tcp":
				markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_UNSUPPORTED_NETWORK)
			default:
				probes = append(probes, publicProbe{result: result, host: endpoint.address})
			}
		}
		response.Diagnostics[index] = diagnostic
	}
	if len(probes) > maximumTCPProbes {
		return nil, ErrTooManyProbes
	}
	if err := service.runProbes(ctx, probes); err != nil {
		return nil, err
	}
	return response, nil
}

func (service *Service) snapshot(nodeID string) (nodeState, bool) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	value, exists := service.nodes[nodeID]
	value.listeners = append([]agentv1.PluginListenerStatus(nil), value.listeners...)
	return value, exists
}

func (service *Service) requirePluginServices(ctx context.Context, nodeID, pluginID string,
	ports []*centerpluginv1.ServicePort,
) error {
	services, err := service.database.ListPluginServices(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("list plugin services for network diagnostics: %w", err)
	}
	known := make(map[string]struct{}, len(services))
	for _, candidate := range services {
		if candidate.PluginID == pluginID {
			known[candidate.ServiceID] = struct{}{}
		}
	}
	for _, port := range ports {
		if _, exists := known[port.ServiceId]; !exists {
			return ErrUnknownService
		}
	}
	return nil
}

type resolvedEndpoint struct {
	value     store.NodeEndpoint
	address   string
	available bool
}

func (service *Service) resolvedEndpoints(ctx context.Context, nodeID string) ([]resolvedEndpoint, error) {
	values, err := service.database.ListNodeEndpoints(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list endpoints for network diagnostics: %w", err)
	}
	addresses, err := service.database.ListNodePublicAddresses(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list public addresses for network diagnostics: %w", err)
	}
	byFamily := make(map[string]string, len(addresses))
	for _, address := range addresses {
		byFamily[address.Family] = address.Address
	}
	result := make([]resolvedEndpoint, 0, len(values))
	for _, value := range values {
		if !value.Enabled {
			continue
		}
		resolved := resolvedEndpoint{value: value}
		switch value.Kind {
		case "direct":
			resolved.address = byFamily[value.SourceFamily]
		case "nat", "domain":
			resolved.address = value.Address
		case "managed_ddns":
			if value.SyncStatus == "synced" {
				resolved.address = value.RecordName
			}
		}
		resolved.available = resolved.address != ""
		result = append(result, resolved)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].value.ID < result[j].value.ID })
	return result, nil
}

func applyLocalListener(diagnostic *centerpluginv1.ServicePortDiagnostic,
	listeners []agentv1.PluginListenerStatus, pluginID string,
) {
	for _, listener := range listeners {
		if listener.PluginID != pluginID || listener.ServiceID != diagnostic.ServiceId ||
			listener.Network != diagnostic.Network || listener.Port != diagnostic.LocalPort {
			continue
		}
		diagnostic.ListenAddress = listener.ListenAddress
		diagnostic.LocalObservedAtUnixNano = listener.ObservedAt.UnixNano()
		switch listener.State {
		case agentv1.ListenerStateListening:
			diagnostic.LocalState = centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_LISTENING
		case agentv1.ListenerStateNotListening:
			diagnostic.LocalState = centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_NOT_LISTENING
		default:
			diagnostic.LocalState = centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_UNKNOWN
		}
		return
	}
}

func markNotTested(result *centerpluginv1.EndpointPortDiagnostic, reason centerpluginv1.PortProbeReason) {
	result.Reachability = centerpluginv1.PortReachability_PORT_REACHABILITY_NOT_TESTED
	result.Reason = reason
}

type publicProbe struct {
	result *centerpluginv1.EndpointPortDiagnostic
	host   string
}

func (service *Service) runProbes(ctx context.Context, values []publicProbe) error {
	if len(values) == 0 {
		return nil
	}
	jobs := make(chan publicProbe)
	workers := probeConcurrency
	if len(values) < workers {
		workers = len(values)
	}
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for value := range jobs {
				service.probe(ctx, value.host, value.result)
			}
		}()
	}
	for _, value := range values {
		select {
		case jobs <- value:
		case <-ctx.Done():
			close(jobs)
			wait.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	wait.Wait()
	return ctx.Err()
}

func (service *Service) probe(parent context.Context, host string, result *centerpluginv1.EndpointPortDiagnostic) {
	ctx, cancel := context.WithTimeout(parent, probeTimeout)
	defer cancel()
	addresses, err := service.resolvePublic(ctx, host)
	if err != nil {
		result.Reachability = centerpluginv1.PortReachability_PORT_REACHABILITY_UNREACHABLE
		result.Reason = centerpluginv1.PortProbeReason_PORT_PROBE_REASON_DNS_FAILED
		return
	}
	if len(addresses) == 0 {
		markNotTested(result, centerpluginv1.PortProbeReason_PORT_PROBE_REASON_ENDPOINT_UNAVAILABLE)
		return
	}
	reason := centerpluginv1.PortProbeReason_PORT_PROBE_REASON_NETWORK_UNREACHABLE
	for _, address := range addresses {
		err = service.dial(ctx, net.JoinHostPort(address.String(), fmt.Sprint(result.Port)))
		if err == nil {
			result.Reachability = centerpluginv1.PortReachability_PORT_REACHABILITY_REACHABLE
			result.Reason = centerpluginv1.PortProbeReason_PORT_PROBE_REASON_UNSPECIFIED
			return
		}
		reason = preferredProbeReason(reason, classifyProbeError(err))
		if ctx.Err() != nil {
			break
		}
	}
	result.Reachability = centerpluginv1.PortReachability_PORT_REACHABILITY_UNREACHABLE
	result.Reason = reason
}

func (service *Service) resolvePublic(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		if publicAddress(address) {
			return []netip.Addr{address}, nil
		}
		return nil, nil
	}
	addresses, err := service.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		if publicAddress(address) {
			result = append(result, address)
		}
	}
	return result, nil
}

func publicAddress(address netip.Addr) bool {
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() &&
		!address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsUnspecified()
}

func classifyProbeError(err error) centerpluginv1.PortProbeReason {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return centerpluginv1.PortProbeReason_PORT_PROBE_REASON_CONNECTION_REFUSED
	case errors.Is(err, context.DeadlineExceeded):
		return centerpluginv1.PortProbeReason_PORT_PROBE_REASON_TIMEOUT
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return centerpluginv1.PortProbeReason_PORT_PROBE_REASON_TIMEOUT
	}
	return centerpluginv1.PortProbeReason_PORT_PROBE_REASON_NETWORK_UNREACHABLE
}

func preferredProbeReason(current, candidate centerpluginv1.PortProbeReason) centerpluginv1.PortProbeReason {
	priority := map[centerpluginv1.PortProbeReason]int{
		centerpluginv1.PortProbeReason_PORT_PROBE_REASON_NETWORK_UNREACHABLE: 1,
		centerpluginv1.PortProbeReason_PORT_PROBE_REASON_TIMEOUT:             2,
		centerpluginv1.PortProbeReason_PORT_PROBE_REASON_CONNECTION_REFUSED:  3,
	}
	if priority[candidate] > priority[current] {
		return candidate
	}
	return current
}
