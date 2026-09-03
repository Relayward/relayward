package ddns

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Relayward/relayward/internal/store"
)

func TestCloudflareSyncCreatesRecord(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			writeCloudflareResponse(t, response, []cloudflareZone{{ID: "zone-id"}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-id/dns_records":
			writeCloudflareResponse(t, response, []cloudflareRecord{})
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone-id/dns_records":
			var value cloudflareRecord
			if err := json.NewDecoder(request.Body).Decode(&value); err != nil {
				t.Error(err)
			}
			if value.Type != "A" || value.Name != "edge.example.com" || value.Content != "203.0.113.10" || value.TTL != 300 || !value.Proxied {
				t.Errorf("created record = %+v", value)
			}
			value.ID = "record-id"
			writeCloudflareResponse(t, response, value)
		default:
			t.Errorf("unexpected Cloudflare request: %s %s", request.Method, request.URL.String())
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	provider := &cloudflareProvider{client: server.Client(), apiBase: server.URL}
	if err := provider.Sync(t.Context(), "test-token", cloudflareEndpoint("ipv4"), "203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("request count = %d", requests)
	}
}

func TestCloudflareSyncUpdatesChangedRecordAndSkipsUnchangedRecord(t *testing.T) {
	for _, test := range []struct {
		name         string
		current      cloudflareRecord
		wantMethod   string
		wantRequests int
	}{
		{
			name: "update", current: cloudflareRecord{ID: "record-id", Type: "AAAA", Name: "edge.example.com", Content: "2001:db8::1", TTL: 60},
			wantMethod: http.MethodPut, wantRequests: 3,
		},
		{
			name: "unchanged", current: cloudflareRecord{ID: "record-id", Type: "AAAA", Name: "edge.example.com", Content: "2001:db8::10", TTL: 300, Proxied: true},
			wantRequests: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			writeMethod := ""
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				requests++
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/zones":
					writeCloudflareResponse(t, response, []cloudflareZone{{ID: "zone-id"}})
				case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-id/dns_records":
					if request.URL.Query().Get("type") != "AAAA" {
						t.Errorf("record type query = %q", request.URL.Query().Get("type"))
					}
					writeCloudflareResponse(t, response, []cloudflareRecord{test.current})
				case request.URL.Path == "/zones/zone-id/dns_records/record-id":
					writeMethod = request.Method
					writeCloudflareResponse(t, response, cloudflareRecord{ID: "record-id"})
				default:
					t.Errorf("unexpected Cloudflare request: %s %s", request.Method, request.URL.String())
					response.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			provider := &cloudflareProvider{client: server.Client(), apiBase: server.URL}
			if err := provider.Sync(t.Context(), "test-token", cloudflareEndpoint("ipv6"), "2001:db8::10"); err != nil {
				t.Fatal(err)
			}
			if requests != test.wantRequests || writeMethod != test.wantMethod {
				t.Fatalf("requests = %d, write method = %q", requests, writeMethod)
			}
		})
	}
}

func TestCloudflareFailureDoesNotExposeToken(t *testing.T) {
	const token = "secret-cloudflare-token"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"bad token secret-cloudflare-token"}],"result":[]}`))
	}))
	t.Cleanup(server.Close)
	provider := &cloudflareProvider{client: server.Client(), apiBase: server.URL}
	err := provider.Sync(t.Context(), token, cloudflareEndpoint("ipv4"), "203.0.113.10")
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("Sync() error = %v", err)
	}
}

func cloudflareEndpoint(family string) store.NodeEndpoint {
	return store.NodeEndpoint{
		SourceFamily: family, ZoneName: "example.com", RecordName: "edge.example.com", TTL: 300, Proxied: true,
	}
}

func writeCloudflareResponse(t *testing.T, response http.ResponseWriter, result any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{"success": true, "errors": []any{}, "result": result}); err != nil {
		t.Error(err)
	}
}
