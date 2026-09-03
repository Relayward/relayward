package server

import (
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type dnsProviderConnectionRequest struct {
	Name     string  `json:"name"`
	Provider string  `json:"provider"`
	APIToken *string `json:"api_token"`
}

type dnsProviderConnectionResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	HasToken  bool      `json:"has_token"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type nodePublicAddressResponse struct {
	Family     string    `json:"family"`
	Address    string    `json:"address"`
	ObservedAt time.Time `json:"observed_at"`
	ReceivedAt time.Time `json:"received_at"`
}

type nodeEndpointRequest struct {
	DisplayName         string                    `json:"display_name"`
	Kind                string                    `json:"kind"`
	Enabled             *bool                     `json:"enabled"`
	SourceFamily        string                    `json:"source_family"`
	Address             string                    `json:"address"`
	PublicPortOverrides store.PublicPortOverrides `json:"public_port_overrides"`
}

type ddnsRecordRequest struct {
	NodeID                  string                    `json:"node_id"`
	DisplayName             string                    `json:"display_name"`
	Enabled                 *bool                     `json:"enabled"`
	SourceFamily            string                    `json:"source_family"`
	PublicPortOverrides     store.PublicPortOverrides `json:"public_port_overrides"`
	DNSProviderConnectionID *string                   `json:"dns_provider_connection_id"`
	ZoneName                string                    `json:"zone_name"`
	RecordName              string                    `json:"record_name"`
	TTL                     int                       `json:"ttl"`
	Proxied                 bool                      `json:"proxied"`
}

type nodeEndpointResponse struct {
	ID                      string                    `json:"id"`
	NodeID                  string                    `json:"node_id"`
	DisplayName             string                    `json:"display_name"`
	Kind                    string                    `json:"kind"`
	Enabled                 bool                      `json:"enabled"`
	SourceFamily            string                    `json:"source_family"`
	Address                 string                    `json:"address"`
	ResolvedAddress         string                    `json:"resolved_address"`
	Available               bool                      `json:"available"`
	PublicPortOverrides     store.PublicPortOverrides `json:"public_port_overrides"`
	DNSProviderConnectionID *string                   `json:"dns_provider_connection_id"`
	ZoneName                string                    `json:"zone_name"`
	RecordName              string                    `json:"record_name"`
	TTL                     int                       `json:"ttl"`
	Proxied                 bool                      `json:"proxied"`
	SyncStatus              string                    `json:"sync_status"`
	ActualAddress           string                    `json:"actual_address"`
	SyncError               string                    `json:"sync_error"`
	SyncedAt                *time.Time                `json:"synced_at"`
	CreatedAt               time.Time                 `json:"created_at"`
	UpdatedAt               time.Time                 `json:"updated_at"`
}

type ddnsRecordResponse struct {
	nodeEndpointResponse
	NodeName string `json:"node_name"`
}

func (server *Server) listNodePublicAddresses(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListNodePublicAddresses(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Node")
		return
	}
	items := make([]nodePublicAddressResponse, len(values))
	for index, value := range values {
		items[index] = nodePublicAddressResponse{Family: value.Family, Address: value.Address, ObservedAt: value.ObservedAt, ReceivedAt: value.ReceivedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) listDNSProviderConnections(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListDNSProviderConnections(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]dnsProviderConnectionResponse, len(values))
	for index, value := range values {
		items[index] = dnsProviderConnectionView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) createDNSProviderConnection(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input dnsProviderConnectionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid DNS provider connection request.", false)
		return
	}
	value, err := server.management.CreateDNSProviderConnection(request.Context(), management.DNSProviderConnectionInput{
		Name: input.Name, Provider: input.Provider, APIToken: input.APIToken,
	})
	if err != nil {
		server.resourceError(w, request, err, "DNS provider connection")
		return
	}
	writeJSON(w, http.StatusCreated, dnsProviderConnectionView(value))
}

func (server *Server) updateDNSProviderConnection(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input dnsProviderConnectionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid DNS provider connection request.", false)
		return
	}
	value, err := server.management.UpdateDNSProviderConnection(request.Context(), request.PathValue("connection_id"), management.DNSProviderConnectionInput{
		Name: input.Name, Provider: input.Provider, APIToken: input.APIToken,
	})
	if err != nil {
		server.resourceError(w, request, err, "DNS provider connection")
		return
	}
	writeJSON(w, http.StatusOK, dnsProviderConnectionView(value))
}

func (server *Server) deleteDNSProviderConnection(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteDNSProviderConnection(request.Context(), request.PathValue("connection_id")); err != nil {
		server.resourceError(w, request, err, "DNS provider connection")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) listDDNSRecords(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListDDNSRecords(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]ddnsRecordResponse, len(values))
	for index, value := range values {
		items[index] = ddnsRecordView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) createDDNSRecord(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, ok := decodeDDNSRecordRequest(w, request)
	if !ok {
		return
	}
	value, err := server.management.CreateDDNSRecord(request.Context(), input)
	if err != nil {
		server.resourceError(w, request, err, "DDNS record")
		return
	}
	writeJSON(w, http.StatusCreated, ddnsRecordView(value))
}

func (server *Server) updateDDNSRecord(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, ok := decodeDDNSRecordRequest(w, request)
	if !ok {
		return
	}
	value, err := server.management.UpdateDDNSRecord(request.Context(), request.PathValue("record_id"), input)
	if err != nil {
		server.resourceError(w, request, err, "DDNS record")
		return
	}
	writeJSON(w, http.StatusOK, ddnsRecordView(value))
}

func (server *Server) deleteDDNSRecord(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteDDNSRecord(request.Context(), request.PathValue("record_id")); err != nil {
		server.resourceError(w, request, err, "DDNS record")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) listNodeEndpoints(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListNodeEndpoints(request.Context(), request.PathValue("node_id"))
	if err != nil {
		server.resourceError(w, request, err, "Node endpoint")
		return
	}
	items := make([]nodeEndpointResponse, len(values))
	for index, value := range values {
		items[index] = nodeEndpointView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) createNodeEndpoint(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, ok := decodeNodeEndpointRequest(w, request)
	if !ok {
		return
	}
	value, err := server.management.CreateNodeEndpoint(request.Context(), request.PathValue("node_id"), input)
	if err != nil {
		server.resourceError(w, request, err, "Node endpoint")
		return
	}
	writeJSON(w, http.StatusCreated, nodeEndpointView(value))
}

func (server *Server) updateNodeEndpoint(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	input, ok := decodeNodeEndpointRequest(w, request)
	if !ok {
		return
	}
	value, err := server.management.UpdateNodeEndpoint(request.Context(), request.PathValue("node_id"), request.PathValue("endpoint_id"), input)
	if err != nil {
		server.resourceError(w, request, err, "Node endpoint")
		return
	}
	writeJSON(w, http.StatusOK, nodeEndpointView(value))
}

func (server *Server) deleteNodeEndpoint(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.DeleteNodeEndpoint(request.Context(), request.PathValue("node_id"), request.PathValue("endpoint_id")); err != nil {
		server.resourceError(w, request, err, "Node endpoint")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeNodeEndpointRequest(w http.ResponseWriter, request *http.Request) (management.NodeEndpointInput, bool) {
	var input nodeEndpointRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node endpoint request.", false)
		return management.NodeEndpointInput{}, false
	}
	if input.Enabled == nil {
		writeProblemWithViolations(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node endpoint request.", false,
			[]protocol.FieldViolation{{Field: "enabled", Description: "is required"}})
		return management.NodeEndpointInput{}, false
	}
	return management.NodeEndpointInput{
		DisplayName: input.DisplayName, Kind: input.Kind, Enabled: *input.Enabled, SourceFamily: input.SourceFamily,
		Address: input.Address, PublicPortOverrides: input.PublicPortOverrides,
	}, true
}

func decodeDDNSRecordRequest(w http.ResponseWriter, request *http.Request) (management.DDNSRecordInput, bool) {
	var input ddnsRecordRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid DDNS record request.", false)
		return management.DDNSRecordInput{}, false
	}
	if input.Enabled == nil {
		writeProblemWithViolations(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid DDNS record request.", false,
			[]protocol.FieldViolation{{Field: "enabled", Description: "is required"}})
		return management.DDNSRecordInput{}, false
	}
	return management.DDNSRecordInput{
		NodeID: input.NodeID, DisplayName: input.DisplayName, Enabled: *input.Enabled, SourceFamily: input.SourceFamily,
		PublicPortOverrides: input.PublicPortOverrides, DNSProviderConnectionID: input.DNSProviderConnectionID,
		ZoneName: input.ZoneName, RecordName: input.RecordName, TTL: input.TTL, Proxied: input.Proxied,
	}, true
}

func dnsProviderConnectionView(value store.DNSProviderConnection) dnsProviderConnectionResponse {
	return dnsProviderConnectionResponse{ID: value.ID, Name: value.Name, Provider: value.Provider, HasToken: value.HasToken,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func nodeEndpointView(value management.NodeEndpointView) nodeEndpointResponse {
	endpoint := value.Endpoint
	ports := endpoint.PublicPortOverrides
	if ports == nil {
		ports = store.PublicPortOverrides{}
	}
	return nodeEndpointResponse{
		ID: endpoint.ID, NodeID: endpoint.NodeID, DisplayName: endpoint.DisplayName, Kind: endpoint.Kind,
		Enabled: endpoint.Enabled, SourceFamily: endpoint.SourceFamily, Address: endpoint.Address,
		ResolvedAddress: value.ResolvedAddress, Available: value.Available, PublicPortOverrides: ports,
		DNSProviderConnectionID: endpoint.DNSProviderConnectionID,
		ZoneName:                endpoint.ZoneName, RecordName: endpoint.RecordName, TTL: endpoint.TTL, Proxied: endpoint.Proxied,
		SyncStatus: endpoint.SyncStatus, ActualAddress: endpoint.ActualAddress, SyncError: endpoint.SyncError,
		SyncedAt: endpoint.SyncedAt, CreatedAt: endpoint.CreatedAt, UpdatedAt: endpoint.UpdatedAt,
	}
}

func ddnsRecordView(value management.DDNSRecordView) ddnsRecordResponse {
	return ddnsRecordResponse{nodeEndpointResponse: nodeEndpointView(value.NodeEndpointView), NodeName: value.NodeName}
}
