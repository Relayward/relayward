package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Relayward/relayward/internal/store"
)

const (
	cloudflareAPIBase    = "https://api.cloudflare.com/client/v4"
	cloudflareBodyLimit  = 1 << 20
	cloudflareAPITimeout = 15 * time.Second
)

type DNSProvider interface {
	Sync(context.Context, string, store.NodeEndpoint, string) error
}

type cloudflareProvider struct {
	client  *http.Client
	apiBase string
}

type cloudflareResponse[T any] struct {
	Success bool              `json:"success"`
	Errors  []cloudflareError `json:"errors"`
	Result  T                 `json:"result"`
}

type cloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudflareZone struct {
	ID string `json:"id"`
}

type cloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func newCloudflareProvider() *cloudflareProvider {
	return &cloudflareProvider{
		client:  &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}, Timeout: cloudflareAPITimeout},
		apiBase: cloudflareAPIBase,
	}
}

func (provider *cloudflareProvider) Sync(ctx context.Context, token string, endpoint store.NodeEndpoint, address string) error {
	zones, err := cloudflareRequest[[]cloudflareZone](provider, ctx, token, http.MethodGet,
		"/zones?name="+url.QueryEscape(endpoint.ZoneName)+"&status=active&per_page=2", nil)
	if err != nil {
		return fmt.Errorf("resolve Cloudflare zone: %w", err)
	}
	if len(zones) != 1 || zones[0].ID == "" {
		return fmt.Errorf("resolve Cloudflare zone: expected exactly one active zone")
	}
	recordType := "A"
	if endpoint.SourceFamily == "ipv6" {
		recordType = "AAAA"
	}
	recordPath := "/zones/" + url.PathEscape(zones[0].ID) + "/dns_records"
	records, err := cloudflareRequest[[]cloudflareRecord](provider, ctx, token, http.MethodGet,
		recordPath+"?type="+recordType+"&name="+url.QueryEscape(endpoint.RecordName)+"&per_page=2", nil)
	if err != nil {
		return fmt.Errorf("resolve Cloudflare DNS record: %w", err)
	}
	if len(records) > 1 {
		return fmt.Errorf("resolve Cloudflare DNS record: expected at most one matching record")
	}
	desired := cloudflareRecord{Type: recordType, Name: endpoint.RecordName, Content: address, TTL: endpoint.TTL, Proxied: endpoint.Proxied}
	if len(records) == 0 {
		if _, err := cloudflareRequest[cloudflareRecord](provider, ctx, token, http.MethodPost, recordPath, desired); err != nil {
			return fmt.Errorf("create Cloudflare DNS record: %w", err)
		}
		return nil
	}
	current := records[0]
	if current.Content == desired.Content && current.TTL == desired.TTL && current.Proxied == desired.Proxied &&
		strings.EqualFold(current.Type, desired.Type) && strings.EqualFold(current.Name, desired.Name) {
		return nil
	}
	if current.ID == "" {
		return errors.New("update Cloudflare DNS record: record ID is missing")
	}
	if _, err := cloudflareRequest[cloudflareRecord](provider, ctx, token, http.MethodPut,
		recordPath+"/"+url.PathEscape(current.ID), desired); err != nil {
		return fmt.Errorf("update Cloudflare DNS record: %w", err)
	}
	return nil
}

func cloudflareRequest[T any](provider *cloudflareProvider, ctx context.Context, token, method, path string, body any) (T, error) {
	var zero T
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(provider.apiBase, "/")+path, reader)
	if err != nil {
		return zero, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return zero, errors.New("Cloudflare API unavailable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, cloudflareBodyLimit+1))
	if err != nil {
		return zero, errors.New("read Cloudflare response")
	}
	if len(raw) > cloudflareBodyLimit {
		return zero, errors.New("Cloudflare response is too large")
	}
	var envelope cloudflareResponse[T]
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return zero, fmt.Errorf("decode Cloudflare response (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return zero, cloudflareFailure(response.StatusCode, envelope.Errors, token)
	}
	return envelope.Result, nil
}

func cloudflareFailure(status int, values []cloudflareError, token string) error {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		message := strings.TrimSpace(value.Message)
		if message == "" {
			continue
		}
		if token != "" {
			message = strings.ReplaceAll(message, token, "[redacted]")
		}
		if value.Code != 0 {
			message = fmt.Sprintf("%d: %s", value.Code, message)
		}
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return fmt.Errorf("Cloudflare API returned HTTP %d", status)
	}
	return fmt.Errorf("Cloudflare API returned HTTP %d: %s", status, strings.Join(parts, "; "))
}
