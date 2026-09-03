package networkdiagnostics

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"

	"github.com/Relayward/relayward/internal/store"
)

const (
	testNodeID   = "10000000-0000-4000-8000-000000000001"
	testPluginID = "io.relayward.test"
)

type databaseStub struct {
	endpoints []store.NodeEndpoint
	addresses []store.NodePublicAddress
	services  []store.PluginService
}

func (database *databaseStub) ListNodeEndpoints(context.Context, string) ([]store.NodeEndpoint, error) {
	return append([]store.NodeEndpoint(nil), database.endpoints...), nil
}

func (database *databaseStub) ListNodePublicAddresses(context.Context, string) ([]store.NodePublicAddress, error) {
	return append([]store.NodePublicAddress(nil), database.addresses...), nil
}

func (database *databaseStub) ListPluginServices(context.Context, string) ([]store.PluginService, error) {
	return append([]store.PluginService(nil), database.services...), nil
}

func TestDiagnoseDistinguishesLocalAndPublicState(t *testing.T) {
	database := &databaseStub{
		services: []store.PluginService{{NodeID: testNodeID, PluginID: testPluginID, ServiceID: "main", Enabled: true}},
		endpoints: []store.NodeEndpoint{{
			ID: "20000000-0000-4000-8000-000000000002", NodeID: testNodeID, DisplayName: "Direct IPv4",
			Kind: "direct", Enabled: true, SourceFamily: "ipv4",
			PublicPortOverrides: store.PublicPortOverrides{testPluginID: {"main": 45142}},
		}},
		addresses: []store.NodePublicAddress{{NodeID: testNodeID, Family: "ipv4", Address: "203.0.113.10"}},
	}
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	dialed := ""
	service.dial = func(_ context.Context, address string) error { dialed = address; return nil }
	service.Activate(testNodeID, "session-1")
	observedAt := time.Now().UTC()
	service.Update(testNodeID, "session-1", []agentv1.PluginListenerStatus{{
		PluginID: testPluginID, ServiceID: "main", Network: "tcp", ListenAddress: "0.0.0.0", Port: 20606,
		State: agentv1.ListenerStateListening, ObservedAt: observedAt,
	}})
	request := &centerpluginv1.DiagnoseNodePortsRequest{NodeId: testNodeID, Ports: []*centerpluginv1.ServicePort{{
		ServiceId: "main", Network: "tcp", Port: 20606,
	}}}
	response, err := service.Diagnose(t.Context(), testPluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := centerpluginv1.ValidateDiagnoseNodePortsResponse(request, response); err != nil {
		t.Fatal(err)
	}
	diagnostic := response.Diagnostics[0]
	if diagnostic.LocalState != centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_LISTENING ||
		len(diagnostic.Endpoints) != 1 ||
		diagnostic.Endpoints[0].Reachability != centerpluginv1.PortReachability_PORT_REACHABILITY_REACHABLE ||
		dialed != "203.0.113.10:45142" {
		t.Fatalf("diagnostic = %+v, endpoint = %+v, dialed = %q", diagnostic, diagnostic.Endpoints[0], dialed)
	}

	dialed = ""
	service.Update(testNodeID, "session-1", []agentv1.PluginListenerStatus{{
		PluginID: testPluginID, ServiceID: "main", Network: "tcp", ListenAddress: "0.0.0.0", Port: 20606,
		State: agentv1.ListenerStateNotListening, ObservedAt: observedAt,
	}})
	response, err = service.Diagnose(t.Context(), testPluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Diagnostics[0].LocalState != centerpluginv1.LocalListenerState_LOCAL_LISTENER_STATE_NOT_LISTENING ||
		response.Diagnostics[0].Endpoints[0].Reason != centerpluginv1.PortProbeReason_PORT_PROBE_REASON_LOCAL_NOT_LISTENING || dialed != "" {
		t.Fatalf("not-listening diagnostic = %+v, dialed = %q", response.Diagnostics[0], dialed)
	}

	service.Deactivate(testNodeID, "session-1")
	response, err = service.Diagnose(t.Context(), testPluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Diagnostics[0].Endpoints[0].Reason != centerpluginv1.PortProbeReason_PORT_PROBE_REASON_NODE_OFFLINE || dialed != "" {
		t.Fatalf("offline diagnostic = %+v, dialed = %q", response.Diagnostics[0], dialed)
	}
}

func TestDiagnoseRejectsPrivateResolvedTargets(t *testing.T) {
	database := &databaseStub{
		services: []store.PluginService{{NodeID: testNodeID, PluginID: testPluginID, ServiceID: "main", Enabled: true}},
		endpoints: []store.NodeEndpoint{{
			ID: "20000000-0000-4000-8000-000000000002", NodeID: testNodeID, DisplayName: "Domain",
			Kind: "domain", Enabled: true, Address: "edge.example.com", PublicPortOverrides: store.PublicPortOverrides{},
		}},
	}
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	service.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.1")}, nil
	}
	dialed := false
	service.dial = func(context.Context, string) error { dialed = true; return nil }
	service.Activate(testNodeID, "session-1")
	request := &centerpluginv1.DiagnoseNodePortsRequest{NodeId: testNodeID, Ports: []*centerpluginv1.ServicePort{{
		ServiceId: "main", Network: "tcp", Port: 20606,
	}}}
	response, err := service.Diagnose(t.Context(), testPluginID, request)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := response.Diagnostics[0].Endpoints[0]
	if dialed || endpoint.Reachability != centerpluginv1.PortReachability_PORT_REACHABILITY_NOT_TESTED ||
		endpoint.Reason != centerpluginv1.PortProbeReason_PORT_PROBE_REASON_ENDPOINT_UNAVAILABLE {
		t.Fatalf("endpoint = %+v, dialed = %t", endpoint, dialed)
	}
}

func TestDiagnoseRejectsProbeBatchesThatCannotFitTheRPCTimeout(t *testing.T) {
	endpoints := make([]store.NodeEndpoint, 49)
	for index := range endpoints {
		endpoints[index] = store.NodeEndpoint{
			ID: fmt.Sprintf("endpoint-%02d", index), NodeID: testNodeID, DisplayName: fmt.Sprintf("Endpoint %d", index),
			Kind: "direct", Enabled: true, SourceFamily: "ipv4", PublicPortOverrides: store.PublicPortOverrides{},
		}
	}
	database := &databaseStub{
		services: []store.PluginService{
			{NodeID: testNodeID, PluginID: testPluginID, ServiceID: "first", Enabled: true},
			{NodeID: testNodeID, PluginID: testPluginID, ServiceID: "second", Enabled: true},
		},
		endpoints: endpoints,
		addresses: []store.NodePublicAddress{{NodeID: testNodeID, Family: "ipv4", Address: "203.0.113.10"}},
	}
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	service.Activate(testNodeID, "session-1")
	request := &centerpluginv1.DiagnoseNodePortsRequest{NodeId: testNodeID, Ports: []*centerpluginv1.ServicePort{
		{ServiceId: "first", Network: "tcp", Port: 20443},
		{ServiceId: "second", Network: "tcp", Port: 30443},
	}}
	if _, err := service.Diagnose(t.Context(), testPluginID, request); !errors.Is(err, ErrTooManyProbes) {
		t.Fatalf("Diagnose() error = %v, want ErrTooManyProbes", err)
	}
}

func TestNodeSessionReplacementDropsStaleListenerState(t *testing.T) {
	service, err := New(&databaseStub{})
	if err != nil {
		t.Fatal(err)
	}
	listener := agentv1.PluginListenerStatus{
		PluginID: testPluginID, ServiceID: "main", Network: "tcp", ListenAddress: "0.0.0.0", Port: 20443,
		State: agentv1.ListenerStateListening, ObservedAt: time.Now().UTC(),
	}
	service.Activate(testNodeID, "session-1")
	service.Update(testNodeID, "session-1", []agentv1.PluginListenerStatus{listener})
	service.Activate(testNodeID, "session-2")
	service.Update(testNodeID, "session-1", []agentv1.PluginListenerStatus{listener})
	service.Deactivate(testNodeID, "session-1")
	state, connected := service.snapshot(testNodeID)
	if !connected || state.sessionID != "session-2" || len(state.listeners) != 0 {
		t.Fatalf("snapshot = %+v, connected = %t", state, connected)
	}
}
