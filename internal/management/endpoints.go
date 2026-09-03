package management

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"unicode"

	"github.com/Relayward/relayward-sdk/contract"
	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const maximumNodeEndpoints = 32

var endpointDomainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var endpointServiceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type DNSProviderConnectionInput struct {
	Name     string
	Provider string
	APIToken *string
}

type NodeEndpointInput struct {
	DisplayName             string
	Kind                    string
	Enabled                 bool
	SourceFamily            string
	Address                 string
	PublicPortOverrides     store.PublicPortOverrides
	DNSProviderConnectionID *string
	ZoneName                string
	RecordName              string
	TTL                     int
	Proxied                 bool
}

type DDNSRecordInput struct {
	NodeID                  string
	DisplayName             string
	Enabled                 bool
	SourceFamily            string
	PublicPortOverrides     store.PublicPortOverrides
	DNSProviderConnectionID *string
	ZoneName                string
	RecordName              string
	TTL                     int
	Proxied                 bool
}

type NodeEndpointView struct {
	Endpoint        store.NodeEndpoint
	ResolvedAddress string
	Available       bool
}

type DDNSRecordView struct {
	NodeEndpointView
	NodeName string
}

func (service *Service) ListNodePublicAddresses(ctx context.Context, nodeID string) ([]store.NodePublicAddress, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return nil, err
	}
	if _, err := service.store.NodeByID(ctx, nodeID); err != nil {
		return nil, err
	}
	return service.store.ListNodePublicAddresses(ctx, nodeID)
}

func (service *Service) ListDNSProviderConnections(ctx context.Context) ([]store.DNSProviderConnection, error) {
	return service.store.ListDNSProviderConnections(ctx)
}

func (service *Service) CreateDNSProviderConnection(ctx context.Context, input DNSProviderConnectionInput) (store.DNSProviderConnection, error) {
	value, token, err := service.normalizeDNSProviderConnection(uuid.NewString(), input, true)
	if err != nil {
		return store.DNSProviderConnection{}, err
	}
	ciphertext, err := service.encryptDNSProviderToken(value.ID, token)
	if err != nil {
		return store.DNSProviderConnection{}, err
	}
	now := service.currentTime()
	value.HasToken = true
	value.CreatedAt, value.UpdatedAt = now, now
	if err := service.store.CreateDNSProviderConnection(ctx, value, ciphertext, now); err != nil {
		return store.DNSProviderConnection{}, err
	}
	return value, nil
}

func (service *Service) UpdateDNSProviderConnection(ctx context.Context, id string, input DNSProviderConnectionInput) (store.DNSProviderConnection, error) {
	if err := validateID("connection_id", id); err != nil {
		return store.DNSProviderConnection{}, err
	}
	current, err := service.store.DNSProviderConnectionByID(ctx, id)
	if err != nil {
		return store.DNSProviderConnection{}, err
	}
	input.Provider = current.Provider
	value, token, err := service.normalizeDNSProviderConnection(id, input, false)
	if err != nil {
		return store.DNSProviderConnection{}, err
	}
	var ciphertext []byte
	if token != "" {
		ciphertext, err = service.encryptDNSProviderToken(id, token)
		if err != nil {
			return store.DNSProviderConnection{}, err
		}
	}
	now := service.currentTime()
	if err := service.store.UpdateDNSProviderConnection(ctx, value, ciphertext, now); err != nil {
		return store.DNSProviderConnection{}, err
	}
	return service.store.DNSProviderConnectionByID(ctx, id)
}

func (service *Service) DeleteDNSProviderConnection(ctx context.Context, id string) error {
	if err := validateID("connection_id", id); err != nil {
		return err
	}
	return service.store.DeleteDNSProviderConnection(ctx, id, service.currentTime())
}

func (service *Service) normalizeDNSProviderConnection(id string, input DNSProviderConnectionInput, tokenRequired bool) (store.DNSProviderConnection, string, error) {
	name, err := normalizedRequired("name", input.Name, 100)
	if err != nil {
		return store.DNSProviderConnection{}, "", err
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider != "cloudflare" {
		return store.DNSProviderConnection{}, "", invalid("provider", "must be cloudflare")
	}
	token := ""
	if input.APIToken != nil {
		token = strings.TrimSpace(*input.APIToken)
	}
	if tokenRequired && token == "" {
		return store.DNSProviderConnection{}, "", invalid("api_token", "is required")
	}
	if len(token) > 4096 {
		return store.DNSProviderConnection{}, "", invalid("api_token", "must contain at most 4096 bytes")
	}
	for _, character := range token {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return store.DNSProviderConnection{}, "", invalid("api_token", "must not contain whitespace or control characters")
		}
	}
	return store.DNSProviderConnection{ID: id, Name: name, Provider: provider}, token, nil
}

func (service *Service) encryptDNSProviderToken(id, token string) ([]byte, error) {
	if service.secrets == nil || !service.secrets.Available() {
		return nil, secretbox.ErrUnavailable
	}
	ciphertext, err := service.secrets.Encrypt(store.DNSProviderSecretOwnerType, id, store.DNSProviderTokenSecret, []byte(token))
	if err != nil {
		return nil, fmt.Errorf("encrypt DNS provider token: %w", err)
	}
	return ciphertext, nil
}

func (service *Service) ListNodeEndpoints(ctx context.Context, nodeID string) ([]NodeEndpointView, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return nil, err
	}
	if _, err := service.store.NodeByID(ctx, nodeID); err != nil {
		return nil, err
	}
	values, err := service.store.ListNodeEndpoints(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	addresses, err := service.store.ListNodePublicAddresses(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	byFamily := make(map[string]string, len(addresses))
	for _, address := range addresses {
		byFamily[address.Family] = address.Address
	}
	result := make([]NodeEndpointView, len(values))
	for index, value := range values {
		resolved, available := resolveNodeEndpoint(value, byFamily)
		result[index] = NodeEndpointView{Endpoint: value, ResolvedAddress: resolved, Available: available}
	}
	return result, nil
}

func (service *Service) CreateNodeEndpoint(ctx context.Context, nodeID string, input NodeEndpointInput) (NodeEndpointView, error) {
	if normalizedEndpointKind(input.Kind) == "managed_ddns" {
		return NodeEndpointView{}, invalid("kind", "managed DDNS records must be configured from the DDNS page")
	}
	return service.createNodeEndpoint(ctx, nodeID, input)
}

func (service *Service) createNodeEndpoint(ctx context.Context, nodeID string, input NodeEndpointInput) (NodeEndpointView, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return NodeEndpointView{}, err
	}
	if _, err := service.store.NodeByID(ctx, nodeID); err != nil {
		return NodeEndpointView{}, err
	}
	current, err := service.store.ListNodeEndpoints(ctx, nodeID)
	if err != nil {
		return NodeEndpointView{}, err
	}
	if len(current) >= maximumNodeEndpoints {
		return NodeEndpointView{}, invalid("node_id", fmt.Sprintf("must have at most %d endpoints", maximumNodeEndpoints))
	}
	value, err := service.normalizeNodeEndpoint(ctx, uuid.NewString(), nodeID, input, nil)
	if err != nil {
		return NodeEndpointView{}, err
	}
	now := service.currentTime()
	value.CreatedAt, value.UpdatedAt = now, now
	if err := service.store.CreateNodeEndpoint(ctx, value, now); err != nil {
		return NodeEndpointView{}, err
	}
	return service.nodeEndpointView(ctx, value)
}

func (service *Service) UpdateNodeEndpoint(ctx context.Context, nodeID, endpointID string, input NodeEndpointInput) (NodeEndpointView, error) {
	if err := validateID("node_id", nodeID); err != nil {
		return NodeEndpointView{}, err
	}
	if err := validateID("endpoint_id", endpointID); err != nil {
		return NodeEndpointView{}, err
	}
	current, err := service.store.NodeEndpointByID(ctx, nodeID, endpointID)
	if err != nil {
		return NodeEndpointView{}, err
	}
	if current.Kind == "managed_ddns" || normalizedEndpointKind(input.Kind) == "managed_ddns" {
		return NodeEndpointView{}, invalid("kind", "managed DDNS records must be configured from the DDNS page")
	}
	return service.updateNodeEndpoint(ctx, current, input)
}

func (service *Service) updateNodeEndpoint(ctx context.Context, current store.NodeEndpoint, input NodeEndpointInput) (NodeEndpointView, error) {
	endpointID, nodeID := current.ID, current.NodeID
	value, err := service.normalizeNodeEndpoint(ctx, endpointID, nodeID, input, &current)
	if err != nil {
		return NodeEndpointView{}, err
	}
	value.CreatedAt = current.CreatedAt
	value.UpdatedAt = service.currentTime()
	if err := service.store.UpdateNodeEndpoint(ctx, value, value.UpdatedAt); err != nil {
		return NodeEndpointView{}, err
	}
	return service.nodeEndpointView(ctx, value)
}

func (service *Service) DeleteNodeEndpoint(ctx context.Context, nodeID, endpointID string) error {
	if err := validateID("node_id", nodeID); err != nil {
		return err
	}
	if err := validateID("endpoint_id", endpointID); err != nil {
		return err
	}
	current, err := service.store.NodeEndpointByID(ctx, nodeID, endpointID)
	if err != nil {
		return err
	}
	if current.Kind == "managed_ddns" {
		return invalid("endpoint_id", "managed DDNS records must be deleted from the DDNS page")
	}
	return service.store.DeleteNodeEndpoint(ctx, nodeID, endpointID, service.currentTime())
}

func (service *Service) ListDDNSRecords(ctx context.Context) ([]DDNSRecordView, error) {
	nodes, err := service.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]DDNSRecordView, 0)
	for _, node := range nodes {
		endpoints, err := service.ListNodeEndpoints(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		for _, endpoint := range endpoints {
			if endpoint.Endpoint.Kind == "managed_ddns" {
				values = append(values, DDNSRecordView{NodeEndpointView: endpoint, NodeName: node.Name})
			}
		}
	}
	return values, nil
}

func (service *Service) CreateDDNSRecord(ctx context.Context, input DDNSRecordInput) (DDNSRecordView, error) {
	endpoint, err := service.createNodeEndpoint(ctx, input.NodeID, ddnsRecordEndpointInput(input))
	if err != nil {
		return DDNSRecordView{}, err
	}
	node, err := service.store.NodeByID(ctx, input.NodeID)
	if err != nil {
		return DDNSRecordView{}, err
	}
	return DDNSRecordView{NodeEndpointView: endpoint, NodeName: node.Name}, nil
}

func (service *Service) UpdateDDNSRecord(ctx context.Context, id string, input DDNSRecordInput) (DDNSRecordView, error) {
	if err := validateID("record_id", id); err != nil {
		return DDNSRecordView{}, err
	}
	current, err := service.store.NodeEndpointByGlobalID(ctx, id)
	if err != nil {
		return DDNSRecordView{}, err
	}
	if current.Kind != "managed_ddns" {
		return DDNSRecordView{}, store.ErrNotFound
	}
	if input.NodeID != current.NodeID {
		return DDNSRecordView{}, invalid("node_id", "cannot be changed after the DDNS record is created")
	}
	endpoint, err := service.updateNodeEndpoint(ctx, current, ddnsRecordEndpointInput(input))
	if err != nil {
		return DDNSRecordView{}, err
	}
	node, err := service.store.NodeByID(ctx, current.NodeID)
	if err != nil {
		return DDNSRecordView{}, err
	}
	return DDNSRecordView{NodeEndpointView: endpoint, NodeName: node.Name}, nil
}

func (service *Service) DeleteDDNSRecord(ctx context.Context, id string) error {
	if err := validateID("record_id", id); err != nil {
		return err
	}
	current, err := service.store.NodeEndpointByGlobalID(ctx, id)
	if err != nil {
		return err
	}
	if current.Kind != "managed_ddns" {
		return store.ErrNotFound
	}
	return service.store.DeleteNodeEndpoint(ctx, current.NodeID, current.ID, service.currentTime())
}

func ddnsRecordEndpointInput(input DDNSRecordInput) NodeEndpointInput {
	return NodeEndpointInput{
		DisplayName: input.DisplayName, Kind: "managed_ddns", Enabled: input.Enabled,
		SourceFamily: input.SourceFamily, PublicPortOverrides: input.PublicPortOverrides,
		DNSProviderConnectionID: input.DNSProviderConnectionID, ZoneName: input.ZoneName,
		RecordName: input.RecordName, TTL: input.TTL, Proxied: input.Proxied,
	}
}

func (service *Service) normalizeNodeEndpoint(ctx context.Context, id, nodeID string, input NodeEndpointInput, current *store.NodeEndpoint) (store.NodeEndpoint, error) {
	displayName, err := normalizedRequired("display_name", input.DisplayName, 100)
	if err != nil {
		return store.NodeEndpoint{}, err
	}
	value := store.NodeEndpoint{
		ID: id, NodeID: nodeID, DisplayName: displayName, Kind: strings.ToLower(strings.TrimSpace(input.Kind)), Enabled: input.Enabled,
		SourceFamily: strings.ToLower(strings.TrimSpace(input.SourceFamily)), Address: strings.ToLower(strings.TrimSpace(input.Address)),
		PublicPortOverrides:     clonePortOverrides(input.PublicPortOverrides),
		DNSProviderConnectionID: input.DNSProviderConnectionID, ZoneName: strings.ToLower(strings.TrimSpace(input.ZoneName)),
		RecordName: strings.ToLower(strings.TrimSpace(input.RecordName)), TTL: input.TTL, Proxied: input.Proxied,
		SyncStatus: "not_applicable",
	}
	if value.TTL == 0 {
		value.TTL = 1
	}
	if err := validatePortOverrides(value.PublicPortOverrides); err != nil {
		return store.NodeEndpoint{}, err
	}
	switch value.Kind {
	case "direct":
		if value.SourceFamily != "ipv4" && value.SourceFamily != "ipv6" {
			return store.NodeEndpoint{}, invalid("source_family", "must be ipv4 or ipv6 for a direct endpoint")
		}
		value.Address, value.DNSProviderConnectionID, value.ZoneName, value.RecordName = "", nil, "", ""
		value.TTL, value.Proxied = 1, false
	case "nat":
		value.SourceFamily, value.DNSProviderConnectionID, value.ZoneName, value.RecordName = "", nil, "", ""
		value.Address, err = normalizeEndpointAddress("address", value.Address, false)
		value.TTL, value.Proxied = 1, false
	case "domain":
		value.SourceFamily, value.DNSProviderConnectionID, value.ZoneName, value.RecordName = "", nil, "", ""
		value.Address, err = normalizeEndpointAddress("address", value.Address, true)
		value.TTL, value.Proxied = 1, false
	case "managed_ddns":
		value.Address = ""
		if value.SourceFamily != "ipv4" && value.SourceFamily != "ipv6" {
			return store.NodeEndpoint{}, invalid("source_family", "must be ipv4 or ipv6 for managed DDNS")
		}
		if value.DNSProviderConnectionID == nil || validateID("dns_provider_connection_id", *value.DNSProviderConnectionID) != nil {
			return store.NodeEndpoint{}, invalid("dns_provider_connection_id", "must identify a DNS provider connection")
		}
		provider, providerErr := service.store.DNSProviderConnectionByID(ctx, *value.DNSProviderConnectionID)
		if providerErr != nil {
			return store.NodeEndpoint{}, invalid("dns_provider_connection_id", "does not exist")
		}
		if provider.Provider != "cloudflare" {
			return store.NodeEndpoint{}, invalid("dns_provider_connection_id", "uses an unsupported provider")
		}
		value.ZoneName, err = normalizeEndpointAddress("zone_name", value.ZoneName, true)
		if err == nil {
			value.RecordName, err = normalizeEndpointAddress("record_name", value.RecordName, true)
		}
		if err == nil && value.RecordName != value.ZoneName && !strings.HasSuffix(value.RecordName, "."+value.ZoneName) {
			err = invalid("record_name", "must belong to the configured zone")
		}
		if value.TTL != 1 && (value.TTL < 60 || value.TTL > 86400) {
			return store.NodeEndpoint{}, invalid("ttl", "must be automatic or between 60 and 86400 seconds")
		}
		if value.Proxied && value.TTL != 1 {
			return store.NodeEndpoint{}, invalid("ttl", "must be automatic when Cloudflare proxying is enabled")
		}
		value.SyncStatus = "pending"
		if current != nil && sameManagedDDNS(*current, value) {
			value.SyncStatus, value.ActualAddress, value.SyncError, value.SyncedAt = current.SyncStatus, current.ActualAddress, current.SyncError, current.SyncedAt
		}
	default:
		return store.NodeEndpoint{}, invalid("kind", "must be direct, nat, domain, or managed_ddns")
	}
	if err != nil {
		return store.NodeEndpoint{}, err
	}
	return value, nil
}

func (service *Service) nodeEndpointView(ctx context.Context, endpoint store.NodeEndpoint) (NodeEndpointView, error) {
	addresses, err := service.store.ListNodePublicAddresses(ctx, endpoint.NodeID)
	if err != nil {
		return NodeEndpointView{}, err
	}
	byFamily := make(map[string]string, len(addresses))
	for _, address := range addresses {
		byFamily[address.Family] = address.Address
	}
	resolved, available := resolveNodeEndpoint(endpoint, byFamily)
	return NodeEndpointView{Endpoint: endpoint, ResolvedAddress: resolved, Available: available}, nil
}

func resolveNodeEndpoint(value store.NodeEndpoint, publicAddresses map[string]string) (string, bool) {
	if !value.Enabled {
		return "", false
	}
	switch {
	case value.Kind == "direct":
		address := publicAddresses[value.SourceFamily]
		return address, address != ""
	case value.Kind == "nat", value.Kind == "domain":
		return value.Address, value.Address != ""
	case value.Kind == "managed_ddns" && value.SyncStatus == "synced":
		return value.RecordName, value.RecordName != ""
	default:
		return "", false
	}
}

func normalizeEndpointAddress(field, raw string, domainOnly bool) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || len(value) > 253 {
		return "", invalid(field, "must be a domain or canonical public IP without a port")
	}
	if address, err := netip.ParseAddr(value); err == nil {
		if domainOnly {
			return "", invalid(field, "must be a domain without a port")
		}
		if address.String() != value || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() {
			return "", invalid(field, "must be a domain or canonical public IP without a port")
		}
		return value, nil
	}
	if !endpointDomainPattern.MatchString(value) {
		return "", invalid(field, "must be a domain or canonical public IP without a port")
	}
	return value, nil
}

func validatePortOverrides(value store.PublicPortOverrides) error {
	count := 0
	for pluginID, services := range value {
		if err := contract.ValidatePluginID(pluginID); err != nil {
			return invalid("public_port_overrides", fmt.Sprintf("contains invalid plugin ID %q", pluginID))
		}
		if len(services) == 0 {
			return invalid("public_port_overrides", fmt.Sprintf("contains no services for plugin %q", pluginID))
		}
		for serviceID, port := range services {
			count++
			if count > 256 {
				return invalid("public_port_overrides", "must contain at most 256 values")
			}
			if !endpointServiceIDPattern.MatchString(serviceID) {
				return invalid("public_port_overrides", fmt.Sprintf("contains invalid service ID %q", serviceID))
			}
			if port == 0 {
				return invalid("public_port_overrides", fmt.Sprintf("port for %q/%q must be between 1 and 65535", pluginID, serviceID))
			}
		}
	}
	return nil
}

func clonePortOverrides(value store.PublicPortOverrides) store.PublicPortOverrides {
	result := make(store.PublicPortOverrides, len(value))
	for pluginID, services := range value {
		result[pluginID] = make(map[string]uint16, len(services))
		for serviceID, port := range services {
			result[pluginID][serviceID] = port
		}
	}
	return result
}

func sameManagedDDNS(first, second store.NodeEndpoint) bool {
	return first.Kind == second.Kind && first.SourceFamily == second.SourceFamily &&
		first.DNSProviderConnectionID != nil && second.DNSProviderConnectionID != nil &&
		*first.DNSProviderConnectionID == *second.DNSProviderConnectionID && first.ZoneName == second.ZoneName &&
		first.RecordName == second.RecordName && first.TTL == second.TTL && first.Proxied == second.Proxied
}

func normalizedEndpointKind(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
