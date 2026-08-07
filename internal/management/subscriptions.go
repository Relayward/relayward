package management

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"sigs.k8s.io/yaml"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/store"
)

const (
	subscriptionTokenLength = 47
	subscriptionCacheFresh  = 5 * time.Minute
	subscriptionCacheMaxAge = 24 * time.Hour
	subscriptionRenderLimit = 20 * time.Second
)

var ErrSubscriptionInactive = errors.New("subscription is inactive")

type RenderedSubscription struct {
	Content    []byte
	RenderedAt time.Time
	Cached     bool
	Settings   store.SystemSettings
}

func (service *Service) Subscription(ctx context.Context, token string) (store.SubscriptionSnapshot, error) {
	if len(token) != subscriptionTokenLength || !strings.HasPrefix(token, "rws_") {
		return store.SubscriptionSnapshot{}, store.ErrNotFound
	}
	return service.store.SubscriptionByTokenHash(ctx, auth.TokenHash(token), service.currentTime())
}

func SubscriptionStatus(snapshot store.SubscriptionSnapshot, now time.Time) string {
	switch {
	case !snapshot.NodeEnabled:
		return "node_disabled"
	case !snapshot.Authorization.Enabled:
		return "disabled"
	case snapshot.Authorization.ExpiresAt != nil && !snapshot.Authorization.ExpiresAt.After(now):
		return "expired"
	case snapshot.Authorization.TrafficLimitBytes != nil && snapshot.TrafficUsedBytes != nil &&
		*snapshot.TrafficUsedBytes >= uint64(*snapshot.Authorization.TrafficLimitBytes):
		return "quota_exceeded"
	default:
		return "active"
	}
}

func (service *Service) RenderSubscription(ctx context.Context, token, format string) (RenderedSubscription, error) {
	if format != store.SubscriptionFormatBase64 && format != store.SubscriptionFormatMihomo && format != store.SubscriptionFormatSingBox {
		return RenderedSubscription{}, invalid("format", "unsupported subscription format")
	}
	snapshot, err := service.Subscription(ctx, token)
	if err != nil {
		return RenderedSubscription{}, err
	}
	if SubscriptionStatus(snapshot, service.currentTime()) != "active" {
		return RenderedSubscription{}, ErrSubscriptionInactive
	}
	lock := service.subscriptionLock(snapshot.Authorization.ID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read after acquiring the per-authorization lock so a concurrent policy or binding update cannot reuse stale input.
	snapshot, err = service.Subscription(ctx, token)
	if err != nil {
		return RenderedSubscription{}, err
	}
	now := service.currentTime()
	if SubscriptionStatus(snapshot, now) != "active" {
		return RenderedSubscription{}, ErrSubscriptionInactive
	}
	digest, err := subscriptionInputDigest(snapshot)
	if err != nil {
		return RenderedSubscription{}, err
	}
	cache, cacheErr := service.store.SubscriptionRenderCache(ctx, snapshot.Authorization.ID, digest)
	if cacheErr != nil && !errors.Is(cacheErr, store.ErrNotFound) {
		return RenderedSubscription{}, cacheErr
	}
	if cacheErr == nil && subscriptionCacheWithin(cache, now, subscriptionCacheFresh) {
		return cachedSubscription(cache, format, true, snapshot.Settings), nil
	}

	renderContext, cancel := context.WithTimeout(ctx, subscriptionRenderLimit)
	rendered, err := service.renderSubscriptionSnapshot(renderContext, snapshot, digest, now)
	cancel()
	if err == nil {
		if err := service.store.SaveSubscriptionRenderCache(ctx, snapshot.Authorization.ID, rendered); err != nil {
			return RenderedSubscription{}, err
		}
		return cachedSubscription(rendered, format, false, snapshot.Settings), nil
	}
	if cacheErr == nil && subscriptionCacheWithin(cache, now, subscriptionCacheMaxAge) {
		return cachedSubscription(cache, format, true, snapshot.Settings), nil
	}
	return RenderedSubscription{}, fmt.Errorf("%w: subscription plugins are unavailable", ErrUpstreamUnavailable)
}

func (service *Service) renderSubscriptionSnapshot(ctx context.Context, snapshot store.SubscriptionSnapshot,
	digest string, now time.Time,
) (store.SubscriptionRenderCache, error) {
	if len(snapshot.Services) > centerpluginv1.MaximumServices {
		return store.SubscriptionRenderCache{}, errors.New("subscription contains too many services")
	}
	if len(snapshot.Services) > 0 && service.pluginRuntime == nil {
		return store.SubscriptionRenderCache{}, ErrUpstreamUnavailable
	}
	contributions := make([]*centerpluginv1.SubscriptionServiceContribution, 0, len(snapshot.Services))
	for start := 0; start < len(snapshot.Services); {
		end := start + 1
		for end < len(snapshot.Services) && snapshot.Services[end].PluginID == snapshot.Services[start].PluginID {
			end++
		}
		pluginID := snapshot.Services[start].PluginID
		request := &centerpluginv1.RenderSubscriptionRequest{
			AuthorizationId: snapshot.Authorization.ID, NodeId: snapshot.Authorization.NodeID,
			Services: make([]*centerpluginv1.SubscriptionServiceBinding, end-start),
		}
		for index, bound := range snapshot.Services[start:end] {
			if bound.PluginState != "active" || bound.PluginHealth != "healthy" ||
				bound.NodeDesiredState != "running" || bound.NodeActualState != "running" {
				return store.SubscriptionRenderCache{}, fmt.Errorf("plugin %s service is not active", pluginID)
			}
			request.Services[index] = &centerpluginv1.SubscriptionServiceBinding{
				ServiceId: bound.ServiceID, DisplayName: bound.DisplayName,
			}
		}
		response, err := service.pluginRuntime.RenderSubscription(ctx, pluginID, request)
		if err != nil {
			return store.SubscriptionRenderCache{}, err
		}
		if err := centerpluginv1.ValidateRenderSubscriptionResponse(request, response); err != nil {
			return store.SubscriptionRenderCache{}, fmt.Errorf("validate plugin %s subscription: %w", pluginID, err)
		}
		contributions = append(contributions, response.Services...)
		start = end
	}
	base64Content, mihomoContent, singBoxContent, err := aggregateSubscription(contributions)
	if err != nil {
		return store.SubscriptionRenderCache{}, err
	}
	return store.SubscriptionRenderCache{
		InputSHA256: digest, Base64: base64Content, Mihomo: mihomoContent,
		SingBox: singBoxContent, RenderedAt: now,
	}, nil
}

func subscriptionInputDigest(snapshot store.SubscriptionSnapshot) (string, error) {
	type serviceInput struct {
		PluginID             string   `json:"plugin_id"`
		ServiceID            string   `json:"service_id"`
		DisplayName          string   `json:"display_name"`
		Capabilities         []string `json:"capabilities"`
		SubscriptionSHA256   string   `json:"subscription_sha256"`
		PluginVersion        string   `json:"plugin_version"`
		NodeDesiredState     string   `json:"node_desired_state"`
		NodeGeneration       uint64   `json:"node_generation"`
		NodeConfigurationSHA string   `json:"node_configuration_sha256"`
	}
	input := struct {
		AuthorizationID        string         `json:"authorization_id"`
		AuthorizationUpdatedAt time.Time      `json:"authorization_updated_at"`
		NodeID                 string         `json:"node_id"`
		Services               []serviceInput `json:"services"`
	}{
		AuthorizationID: snapshot.Authorization.ID, AuthorizationUpdatedAt: snapshot.Authorization.UpdatedAt,
		NodeID: snapshot.Authorization.NodeID,
	}
	input.Services = make([]serviceInput, len(snapshot.Services))
	for index, value := range snapshot.Services {
		input.Services[index] = serviceInput{
			PluginID: value.PluginID, ServiceID: value.ServiceID, DisplayName: value.DisplayName,
			Capabilities: append([]string(nil), value.Capabilities...), SubscriptionSHA256: value.SubscriptionSHA256,
			PluginVersion: value.PluginVersion, NodeDesiredState: value.NodeDesiredState,
			NodeGeneration: value.NodeGeneration, NodeConfigurationSHA: value.NodeConfigurationSHA,
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode subscription input digest: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func subscriptionCacheWithin(value store.SubscriptionRenderCache, now time.Time, maximumAge time.Duration) bool {
	return !value.RenderedAt.After(now) && now.Sub(value.RenderedAt) <= maximumAge
}

func aggregateSubscription(services []*centerpluginv1.SubscriptionServiceContribution) ([]byte, []byte, []byte, error) {
	uriSet := make(map[string]struct{})
	mihomoSet := make(map[string]json.RawMessage)
	singBoxSet := make(map[string]json.RawMessage)
	totalBytes := 0
	for _, service := range services {
		for _, uri := range service.Uris {
			totalBytes += len(uri)
			if totalBytes > centerpluginv1.MaximumSubscriptionBytes {
				return nil, nil, nil, errors.New("subscription fragments exceed the aggregate size limit")
			}
			uriSet[uri] = struct{}{}
		}
		for _, item := range []struct {
			values [][]byte
			target map[string]json.RawMessage
		}{{service.MihomoProxiesJson, mihomoSet}, {service.SingBoxOutboundsJson, singBoxSet}} {
			for _, raw := range item.values {
				totalBytes += len(raw)
				if totalBytes > centerpluginv1.MaximumSubscriptionBytes {
					return nil, nil, nil, errors.New("subscription fragments exceed the aggregate size limit")
				}
				canonical, err := canonicalJSONObject(raw)
				if err != nil {
					return nil, nil, nil, err
				}
				item.target[string(canonical)] = canonical
			}
		}
	}
	uris := make([]string, 0, len(uriSet))
	for uri := range uriSet {
		uris = append(uris, uri)
	}
	sort.Strings(uris)
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(uris, "\n")))
	base64Content := []byte(encoded + "\n")
	mihomoObjects := sortedRawMessages(mihomoSet)
	mihomoJSON, err := json.Marshal(struct {
		Proxies []json.RawMessage `json:"proxies"`
	}{Proxies: mihomoObjects})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode Mihomo subscription: %w", err)
	}
	mihomoContent, err := yaml.JSONToYAML(mihomoJSON)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode Mihomo YAML subscription: %w", err)
	}
	singBoxContent, err := json.MarshalIndent(struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}{Outbounds: sortedRawMessages(singBoxSet)}, "", "  ")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode sing-box subscription: %w", err)
	}
	singBoxContent = append(singBoxContent, '\n')
	return base64Content, mihomoContent, singBoxContent, nil
}

func canonicalJSONObject(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, errors.New("subscription fragment must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("subscription fragment contains trailing JSON")
		}
		return nil, errors.New("subscription fragment contains invalid trailing data")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize subscription fragment: %w", err)
	}
	return canonical, nil
}

func sortedRawMessages(values map[string]json.RawMessage) []json.RawMessage {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]json.RawMessage, len(keys))
	for index, key := range keys {
		result[index] = values[key]
	}
	return result
}

func cachedSubscription(value store.SubscriptionRenderCache, format string, cached bool, settings store.SystemSettings) RenderedSubscription {
	var content []byte
	switch format {
	case store.SubscriptionFormatBase64:
		content = value.Base64
	case store.SubscriptionFormatMihomo:
		content = value.Mihomo
	case store.SubscriptionFormatSingBox:
		content = value.SingBox
	}
	return RenderedSubscription{Content: append([]byte(nil), content...), RenderedAt: value.RenderedAt, Cached: cached, Settings: settings}
}

func (service *Service) ListPluginServices(ctx context.Context, nodeID string) ([]store.PluginService, error) {
	if nodeID != "" {
		if err := validateID("node_id", nodeID); err != nil {
			return nil, err
		}
		if _, err := service.store.NodeByID(ctx, nodeID); err != nil {
			return nil, err
		}
	}
	return service.store.ListPluginServices(ctx, nodeID)
}

func (service *Service) Announcement(ctx context.Context) (*string, error) {
	return service.store.Announcement(ctx)
}

func (service *Service) UpdateAnnouncement(ctx context.Context, content string) (*string, error) {
	normalized, err := normalizedMultiline("content", content, 4096)
	if err != nil {
		return nil, err
	}
	if err := service.store.UpdateAnnouncement(ctx, normalized, service.currentTime()); err != nil {
		return nil, err
	}
	if normalized == "" {
		return nil, nil
	}
	return &normalized, nil
}
