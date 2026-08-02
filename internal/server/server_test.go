package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	policyv1 "github.com/Relayward/relayward-sdk/policy/v1"
	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

func TestHealthAndSystemInfo(t *testing.T) {
	handler, _ := newTestHandler(t)

	health := performRequest(handler, http.MethodGet, "/healthz", nil, nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}
	var healthBody map[string]string
	decodeResponse(t, health, &healthBody)
	if healthBody["status"] != "ok" {
		t.Fatalf("health body = %+v", healthBody)
	}

	info := performRequest(handler, http.MethodGet, "/api/v1/system/info", nil, nil)
	var infoBody systemInfo
	decodeResponse(t, info, &infoBody)
	if info.Code != http.StatusOK || infoBody.Name != "relayward" || infoBody.Version != "test" || !infoBody.SecretsAvailable {
		t.Fatalf("system info status = %d, body = %+v", info.Code, infoBody)
	}
}

func TestWebAssetsAndSPAFallback(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":         {Data: []byte("<!doctype html><main>Relayward</main>")},
		"assets/app-abcd.js": {Data: []byte("console.log('relayward')")},
	}
	handler := newTestHandlerWithWebAssets(t, assets)

	for _, requestPath := range []string{"/", "/nodes", "/nodes/", "/authorizations/example"} {
		response := performRequest(handler, http.MethodGet, requestPath, nil, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Relayward") {
			t.Fatalf("GET %s = %d, %q", requestPath, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-src 'self'") {
			t.Fatalf("GET %s headers = %+v", requestPath, response.Header())
		}
	}

	asset := performRequest(handler, http.MethodGet, "/assets/app-abcd.js", nil, nil)
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log('relayward')" ||
		asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.Contains(asset.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("asset response = %d, headers=%+v, body=%q", asset.Code, asset.Header(), asset.Body.String())
	}
	head := performRequest(handler, http.MethodHead, "/assets/app-abcd.js", nil, nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD asset = %d, headers=%+v, body=%q", head.Code, head.Header(), head.Body.String())
	}

	for _, requestPath := range []string{"/assets/missing.js", "/api/v1/missing", "/assets/%2e%2e/index.html"} {
		response := performRequest(handler, http.MethodGet, requestPath, nil, nil)
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "Relayward") {
			t.Fatalf("GET %s unexpectedly served the SPA: %d, %q", requestPath, response.Code, response.Body.String())
		}
	}
	apiMissing := performRequest(handler, http.MethodGet, "/api/v1/missing", nil, nil)
	if apiMissing.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("unknown API CSP = %q", apiMissing.Header().Get("Content-Security-Policy"))
	}
	postRoute := performRequest(handler, http.MethodPost, "/nodes", nil, nil)
	if postRoute.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST SPA route status = %d", postRoute.Code)
	}
}

func TestSetupSessionCSRFAndLogout(t *testing.T) {
	handler, _ := newTestHandler(t)

	status := performRequest(handler, http.MethodGet, "/api/v1/setup", nil, nil)
	var statusBody map[string]bool
	decodeResponse(t, status, &statusBody)
	if statusBody["initialized"] {
		t.Fatal("fresh instance is initialized")
	}

	invalid := performRequest(handler, http.MethodPost, "/api/v1/setup", []byte(`{"username":"admin"}`), map[string]string{"Content-Type": "text/plain"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid setup status = %d", invalid.Code)
	}

	setup := performRequest(handler, http.MethodPost, "/api/v1/setup",
		[]byte(`{"username":"admin","password":"correct horse battery staple"}`),
		map[string]string{"Content-Type": "application/json"})
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setup.Code, setup.Body.String())
	}
	cookies := setup.Result().Cookies()
	sessionCookie := cookieByName(t, cookies, sessionCookieName)
	csrfCookie := cookieByName(t, cookies, csrfCookieName)
	if !sessionCookie.HttpOnly || csrfCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected cookie settings: session=%+v csrf=%+v", sessionCookie, csrfCookie)
	}

	session := performRequest(handler, http.MethodGet, "/api/v1/auth/session", nil, nil, sessionCookie)
	if session.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", session.Code, session.Body.String())
	}

	withoutCSRF := performRequest(handler, http.MethodPost, "/api/v1/auth/logout", nil, nil, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF status = %d", withoutCSRF.Code)
	}

	logout := performRequest(handler, http.MethodPost, "/api/v1/auth/logout", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, body = %s", logout.Code, logout.Body.String())
	}
	expired := performRequest(handler, http.MethodGet, "/api/v1/auth/session", nil, nil, sessionCookie)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired session status = %d", expired.Code)
	}
}

func TestLoginUsesStandardProblem(t *testing.T) {
	handler, _ := newTestHandler(t)
	setup := performRequest(handler, http.MethodPost, "/api/v1/setup",
		[]byte(`{"username":"admin","password":"correct horse battery staple"}`),
		map[string]string{"Content-Type": "application/json"})
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d", setup.Code)
	}

	login := performRequest(handler, http.MethodPost, "/api/v1/auth/login",
		[]byte(`{"username":"admin","password":"wrong password"}`),
		map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d", login.Code)
	}
	var problem protocol.Problem
	decodeResponse(t, login, &problem)
	if problem.Code != protocol.ErrorUnauthenticated || problem.Message != "Invalid credentials." {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestTOTPHTTPFlow(t *testing.T) {
	handler, _ := newTestHandler(t)
	setup := performRequest(handler, http.MethodPost, "/api/v1/setup",
		[]byte(`{"username":"admin","password":"correct horse battery staple"}`),
		map[string]string{"Content-Type": "application/json"})
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setup.Code, setup.Body.String())
	}
	sessionCookie := cookieByName(t, setup.Result().Cookies(), sessionCookieName)
	csrfCookie := cookieByName(t, setup.Result().Cookies(), csrfCookieName)
	jsonHeaders := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}

	withoutCSRF := performRequest(handler, http.MethodPost, "/api/v1/auth/totp/prepare", nil, nil, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("prepare without CSRF status = %d", withoutCSRF.Code)
	}
	prepare := performRequest(handler, http.MethodPost, "/api/v1/auth/totp/prepare", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if prepare.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", prepare.Code, prepare.Body.String())
	}
	var preparation map[string]string
	decodeResponse(t, prepare, &preparation)
	code := testTOTPCode(t, preparation["secret"], time.Now())

	enable := performRequest(handler, http.MethodPost, "/api/v1/auth/totp/enable",
		[]byte(fmt.Sprintf(`{"code":%q}`, code)), jsonHeaders, sessionCookie)
	if enable.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", enable.Code, enable.Body.String())
	}
	var initialCodes map[string][]string
	decodeResponse(t, enable, &initialCodes)
	if len(initialCodes["recovery_codes"]) != 10 {
		t.Fatalf("initial recovery code count = %d", len(initialCodes["recovery_codes"]))
	}

	wrongPassword := performRequest(handler, http.MethodPost, "/api/v1/auth/recovery-codes/regenerate",
		[]byte(fmt.Sprintf(`{"password":"wrong password","second_factor":%q}`, initialCodes["recovery_codes"][0])),
		jsonHeaders, sessionCookie)
	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("regenerate with wrong password status = %d", wrongPassword.Code)
	}
	regenerate := performRequest(handler, http.MethodPost, "/api/v1/auth/recovery-codes/regenerate",
		[]byte(fmt.Sprintf(`{"password":"correct horse battery staple","second_factor":%q}`, initialCodes["recovery_codes"][0])),
		jsonHeaders, sessionCookie)
	if regenerate.Code != http.StatusOK {
		t.Fatalf("regenerate status = %d, body = %s", regenerate.Code, regenerate.Body.String())
	}
	var replacementCodes map[string][]string
	decodeResponse(t, regenerate, &replacementCodes)
	if len(replacementCodes["recovery_codes"]) != 10 {
		t.Fatalf("replacement recovery code count = %d", len(replacementCodes["recovery_codes"]))
	}

	disable := performRequest(handler, http.MethodPost, "/api/v1/auth/totp/disable",
		[]byte(fmt.Sprintf(`{"password":"correct horse battery staple","second_factor":%q}`, replacementCodes["recovery_codes"][0])),
		jsonHeaders, sessionCookie)
	if disable.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d, body = %s", disable.Code, disable.Body.String())
	}
	if cookieByName(t, disable.Result().Cookies(), sessionCookieName).MaxAge != -1 {
		t.Fatal("disable did not clear the session cookie")
	}
	revoked := performRequest(handler, http.MethodGet, "/api/v1/auth/session", nil, nil, sessionCookie)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("session after TOTP disable status = %d", revoked.Code)
	}
}

func TestNodeAndUserHTTPFlow(t *testing.T) {
	handler, _ := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	jsonHeaders := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}

	unauthenticated := performRequest(handler, http.MethodGet, "/api/v1/nodes", nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated node list status = %d", unauthenticated.Code)
	}
	withoutCSRF := performRequest(handler, http.MethodPost, "/api/v1/nodes",
		[]byte(`{"name":"Edge One"}`), map[string]string{"Content-Type": "application/json"}, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("node creation without CSRF status = %d", withoutCSRF.Code)
	}
	createNode := performRequest(handler, http.MethodPost, "/api/v1/nodes",
		[]byte(`{"name":"Edge One","public_address":"edge.example.com"}`), jsonHeaders, sessionCookie)
	if createNode.Code != http.StatusCreated {
		t.Fatalf("create node status = %d, body = %s", createNode.Code, createNode.Body.String())
	}
	var node nodeResponse
	decodeResponse(t, createNode, &node)
	if !node.Enabled || node.Name != "Edge One" {
		t.Fatalf("created node = %+v", node)
	}
	duplicateNode := performRequest(handler, http.MethodPost, "/api/v1/nodes",
		[]byte(`{"name":"edge one"}`), jsonHeaders, sessionCookie)
	if duplicateNode.Code != http.StatusConflict {
		t.Fatalf("duplicate node status = %d", duplicateNode.Code)
	}
	token := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+node.ID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if token.Code != http.StatusCreated {
		t.Fatalf("registration token status = %d, body = %s", token.Code, token.Body.String())
	}
	var tokenBody map[string]any
	decodeResponse(t, token, &tokenBody)
	if value, ok := tokenBody["token"].(string); !ok || !strings.HasPrefix(value, "rwr_") {
		t.Fatalf("registration token response = %+v", tokenBody)
	}
	updateNode := performRequest(handler, http.MethodPut, "/api/v1/nodes/"+node.ID,
		[]byte(`{"name":"Edge Renamed","public_address":"","enabled":false}`), jsonHeaders, sessionCookie)
	if updateNode.Code != http.StatusOK {
		t.Fatalf("update node status = %d, body = %s", updateNode.Code, updateNode.Body.String())
	}

	invalidUser := performRequest(handler, http.MethodPost, "/api/v1/users",
		[]byte(`{"display_name":"Alice","email":"not-an-email","telegram":null,"note":""}`), jsonHeaders, sessionCookie)
	if invalidUser.Code != http.StatusBadRequest {
		t.Fatalf("invalid user status = %d", invalidUser.Code)
	}
	var invalidProblem protocol.Problem
	decodeResponse(t, invalidUser, &invalidProblem)
	if len(invalidProblem.Violations) != 1 || invalidProblem.Violations[0].Field != "email" {
		t.Fatalf("invalid user problem = %+v", invalidProblem)
	}
	createUser := performRequest(handler, http.MethodPost, "/api/v1/users",
		[]byte(`{"display_name":"Alice","email":"alice@example.com","telegram":"@alice","note":"customer"}`), jsonHeaders, sessionCookie)
	if createUser.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", createUser.Code, createUser.Body.String())
	}
	var user userResponse
	decodeResponse(t, createUser, &user)
	listUsers := performRequest(handler, http.MethodGet, "/api/v1/users", nil, nil, sessionCookie)
	if listUsers.Code != http.StatusOK || !strings.Contains(listUsers.Body.String(), user.ID) {
		t.Fatalf("list users status = %d, body = %s", listUsers.Code, listUsers.Body.String())
	}
	deleteUser := performRequest(handler, http.MethodDelete, "/api/v1/users/"+user.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if deleteUser.Code != http.StatusNoContent {
		t.Fatalf("delete user status = %d", deleteUser.Code)
	}
	deleteNode := performRequest(handler, http.MethodDelete, "/api/v1/nodes/"+node.ID, nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if deleteNode.Code != http.StatusNoContent {
		t.Fatalf("delete node status = %d", deleteNode.Code)
	}
}

func TestAuthorizationBindingAndAuditHTTPFlow(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}

	nodeRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`), headers, sessionCookie)
	var node nodeResponse
	decodeResponse(t, nodeRequest, &node)
	userRequest := performRequest(handler, http.MethodPost, "/api/v1/users",
		[]byte(`{"display_name":"Alice","email":null,"telegram":null,"note":""}`), headers, sessionCookie)
	var user userResponse
	decodeResponse(t, userRequest, &user)

	body := fmt.Sprintf(`{
      "user_id":%q,"node_id":%q,"enabled":true,"traffic_limit_bytes":null,
      "reset":{"kind":"never","value":null,"timezone":"UTC","period_anchor":null},
      "expires_at":null,"soft_ip_limit":null,"activity_window_seconds":600,"block_duration_seconds":1800
    }`, user.ID, node.ID)
	create := performRequest(handler, http.MethodPost, "/api/v1/authorizations", []byte(body), headers, sessionCookie)
	if create.Code != http.StatusCreated {
		t.Fatalf("create authorization status = %d, body = %s", create.Code, create.Body.String())
	}
	var created struct {
		Authorization     authorizationResponse `json:"authorization"`
		SubscriptionToken string                `json:"subscription_token"`
	}
	decodeResponse(t, create, &created)
	if !strings.HasPrefix(created.SubscriptionToken, "rws_") {
		t.Fatalf("subscription token = %q", created.SubscriptionToken)
	}
	period, err := policyv1.CurrentPeriod(policyv1.ResetRule{Kind: policyv1.ResetNever, Timezone: "UTC"},
		created.Authorization.CreatedAt, time.Now().UTC())
	if err != nil {
		t.Fatalf("compute authorization period: %v", err)
	}
	if err := database.ApplyTrafficSnapshot(t.Context(), node.ID, agentv1.TrafficSnapshotEvent{
		AuthorizationID: created.Authorization.ID, Period: period, Revision: 1,
		UploadBytes: 1024, DownloadBytes: 2048,
	}, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("apply authorization traffic: %v", err)
	}
	if err := database.RecordAuthorizationPolicyStatus(t.Context(), node.ID, agentv1.PolicyStatusEvent{
		Generation: 2, AuthorizationID: created.Authorization.ID, Period: period,
		UploadBytes: 1024, DownloadBytes: 2048, ServicesEnabled: true,
		Reason: agentv1.PolicyReasonActive, ActiveIPCount: 1,
	}, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("record authorization enforcement: %v", err)
	}
	authorizationStatus := performRequest(handler, http.MethodGet,
		"/api/v1/authorizations/"+created.Authorization.ID, nil, nil, sessionCookie)
	var enriched authorizationResponse
	decodeResponse(t, authorizationStatus, &enriched)
	if authorizationStatus.Code != http.StatusOK || enriched.CurrentTraffic == nil ||
		enriched.CurrentTraffic.DownloadBytes != 2048 || enriched.Enforcement == nil ||
		enriched.Enforcement.Generation != 2 || !enriched.Enforcement.ServicesEnabled {
		t.Fatalf("enriched authorization status = %d, %+v", authorizationStatus.Code, enriched)
	}
	publicSubscription := performRequest(handler, http.MethodGet,
		"/api/v1/subscriptions/"+created.SubscriptionToken, nil, nil)
	if publicSubscription.Code != http.StatusOK || !strings.Contains(publicSubscription.Body.String(), `"status":"active"`) {
		t.Fatalf("public subscription status = %d, body = %s", publicSubscription.Code, publicSubscription.Body.String())
	}
	if strings.Contains(publicSubscription.Body.String(), created.SubscriptionToken) {
		t.Fatal("public subscription response contains its raw token")
	}
	duplicate := performRequest(handler, http.MethodPost, "/api/v1/authorizations", []byte(body), headers, sessionCookie)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate authorization status = %d", duplicate.Code)
	}

	invalidBody := strings.Replace(body, `"kind":"never","value":null`, `"kind":"weekly","value":null`, 1)
	invalid := performRequest(handler, http.MethodPut, "/api/v1/authorizations/"+created.Authorization.ID,
		[]byte(invalidBody), headers, sessionCookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid authorization update status = %d", invalid.Code)
	}

	binding := performRequest(handler, http.MethodPost,
		"/api/v1/authorizations/"+created.Authorization.ID+"/service-bindings",
		[]byte(`{"plugin_id":"xray-runtime","service_id":"vless-main","enabled":true}`), headers, sessionCookie)
	if binding.Code != http.StatusNotFound {
		t.Fatalf("create binding without plugin service status = %d, body = %s", binding.Code, binding.Body.String())
	}

	rotate := performRequest(handler, http.MethodPost,
		"/api/v1/authorizations/"+created.Authorization.ID+"/subscription-token", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate token status = %d, body = %s", rotate.Code, rotate.Body.String())
	}
	if strings.Contains(rotate.Body.String(), created.SubscriptionToken) {
		t.Fatal("rotation returned the previous subscription token")
	}
	oldSubscription := performRequest(handler, http.MethodGet,
		"/api/v1/subscriptions/"+created.SubscriptionToken, nil, nil)
	if oldSubscription.Code != http.StatusNotFound {
		t.Fatalf("rotated subscription token status = %d", oldSubscription.Code)
	}

	audit := performRequest(handler, http.MethodGet, "/api/v1/audit?limit=200", nil, nil, sessionCookie)
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), "authorization.subscription_token.rotate") {
		t.Fatalf("audit status = %d, body = %s", audit.Code, audit.Body.String())
	}
	if strings.Contains(audit.Body.String(), created.SubscriptionToken) {
		t.Fatal("audit response contains a raw subscription token")
	}
}

func TestRecentAccessEventsHTTPFlow(t *testing.T) {
	handler, _, events := newTestHandlerWithEventStore(t)
	unauthenticated := performRequest(handler, http.MethodGet, "/api/v1/events/access", nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated access events status = %d", unauthenticated.Code)
	}
	sessionCookie, csrfCookie := setupCookies(t, handler)
	nodeRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`),
		map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	var node nodeResponse
	decodeResponse(t, nodeRequest, &node)

	observedAt := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	streamID := "0123456789abcdef0123456789abcdef"
	for index, action := range []string{agentv1.AccessActionAccepted, agentv1.AccessActionBlocked} {
		access := agentv1.AccessEvent{
			SourceStreamID: streamID, SourceEventID: fmt.Sprintf("runtime-%d", index+1),
			PluginID: "runtime.test", ServiceID: "vless-main",
			AuthorizationID: "30000000-0000-4000-8000-000000000003", SourceIP: "192.0.2.10",
			Destination: "example.com", DestinationPort: 443, Network: "tcp", Protocol: "tls", Action: action,
		}
		event, err := agentv1.NewEvent(node.ID, streamID, uint64(index+1), agentv1.EventAccess, observedAt.Add(time.Duration(index)*time.Second), access)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := events.StoreAccessEvent(t.Context(), eventstore.StoredEvent{
			RowID: int64(index + 1), NodeID: node.ID, StreamID: streamID, Event: event, ReceivedAt: observedAt.Add(time.Minute),
		}, access); err != nil {
			t.Fatalf("StoreAccessEvent() error = %v", err)
		}
	}

	invalid := performRequest(handler, http.MethodGet, "/api/v1/events/access?limit=501", nil, nil, sessionCookie)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid access event query status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
	unknown := performRequest(handler, http.MethodGet,
		"/api/v1/events/access?node_id=40000000-0000-4000-8000-000000000004", nil, nil, sessionCookie)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown access event node status = %d", unknown.Code)
	}
	firstPage := performRequest(handler, http.MethodGet,
		"/api/v1/events/access?node_id="+node.ID+"&limit=1", nil, nil, sessionCookie)
	if firstPage.Code != http.StatusOK {
		t.Fatalf("first access event page status = %d, body = %s", firstPage.Code, firstPage.Body.String())
	}
	var first struct {
		Items []accessEventResponse `json:"items"`
	}
	decodeResponse(t, firstPage, &first)
	if len(first.Items) != 1 || first.Items[0].Action != agentv1.AccessActionBlocked {
		t.Fatalf("first access event page = %+v", first.Items)
	}
	if strings.Contains(firstPage.Body.String(), "source_stream_id") || strings.Contains(firstPage.Body.String(), "agent_event_id") || strings.Contains(firstPage.Body.String(), "payload_sha256") {
		t.Fatal("access event response exposes internal deduplication fields")
	}
	secondPage := performRequest(handler, http.MethodGet,
		fmt.Sprintf("/api/v1/events/access?node_id=%s&before_id=%d&limit=1", node.ID, first.Items[0].ID), nil, nil, sessionCookie)
	var second struct {
		Items []accessEventResponse `json:"items"`
	}
	decodeResponse(t, secondPage, &second)
	if secondPage.Code != http.StatusOK || len(second.Items) != 1 || second.Items[0].Action != agentv1.AccessActionAccepted {
		t.Fatalf("second access event page status = %d, items = %+v", secondPage.Code, second.Items)
	}
}

func TestSubscriptionStatus(t *testing.T) {
	now := time.Date(2026, time.August, 2, 10, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	future := now.Add(time.Second)
	tests := []struct {
		name     string
		snapshot store.SubscriptionSnapshot
		want     string
	}{
		{name: "active", snapshot: store.SubscriptionSnapshot{NodeEnabled: true, Authorization: store.Authorization{Enabled: true}}, want: "active"},
		{name: "future expiry", snapshot: store.SubscriptionSnapshot{NodeEnabled: true, Authorization: store.Authorization{Enabled: true, ExpiresAt: &future}}, want: "active"},
		{name: "expired", snapshot: store.SubscriptionSnapshot{NodeEnabled: true, Authorization: store.Authorization{Enabled: true, ExpiresAt: &expired}}, want: "expired"},
		{name: "disabled authorization", snapshot: store.SubscriptionSnapshot{NodeEnabled: true}, want: "disabled"},
		{name: "disabled node", snapshot: store.SubscriptionSnapshot{}, want: "node_disabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := management.SubscriptionStatus(test.snapshot, now); got != test.want {
				t.Fatalf("SubscriptionStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAgentRegistrationControlAndDuplicateSession(t *testing.T) {
	handler, database := newTestHandler(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	nodeRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`), headers, sessionCookie)
	if nodeRequest.Code != http.StatusCreated {
		t.Fatalf("create node status = %d, body = %s", nodeRequest.Code, nodeRequest.Body.String())
	}
	var node nodeResponse
	decodeResponse(t, nodeRequest, &node)
	tokenRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+node.ID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if tokenRequest.Code != http.StatusCreated {
		t.Fatalf("create registration token status = %d", tokenRequest.Code)
	}
	var token struct {
		Value string `json:"token"`
	}
	decodeResponse(t, tokenRequest, &token)
	registrationBody, err := json.Marshal(agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: token.Value, AgentVersion: "0.1.0",
		Hostname: "edge-one", OS: "linux", Arch: "amd64", Capabilities: []string{"control.heartbeat"},
	})
	if err != nil {
		t.Fatalf("marshal registration request: %v", err)
	}
	registration := performRequest(handler, http.MethodPost, "/api/v1/agent/register", registrationBody,
		map[string]string{"Content-Type": "application/json"})
	if registration.Code != http.StatusCreated {
		t.Fatalf("Agent registration status = %d, body = %s", registration.Code, registration.Body.String())
	}
	var identity agentv1.RegisterResponse
	decodeResponse(t, registration, &identity)
	if err := agentv1.ValidateRegisterResponse(identity); err != nil {
		t.Fatalf("Agent registration response = %+v: %v", identity, err)
	}
	reused := performRequest(handler, http.MethodPost, "/api/v1/agent/register", registrationBody,
		map[string]string{"Content-Type": "application/json"})
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused registration token status = %d", reused.Code)
	}
	now := time.Now().UTC()
	heartbeatOnlyCommand, err := database.CreateAgentCommand(context.Background(), "heartbeat-only-command", node.ID, agentv1.Command{
		Kind: "agent.test", IssuedAt: now, ExpiresAt: now.Add(time.Hour), Payload: json.RawMessage(`{}`),
	}, now)
	if err != nil {
		t.Fatalf("create command for capability gate: %v", err)
	}

	server := httptest.NewServer(handler)
	defer server.Close()
	first := connectTestAgent(t, server.URL, identity, "0.1.0")
	defer first.Close()
	firstHello := readTestAgentEnvelope(t, first)
	firstSession, err := agentv1.DecodeEnvelopePayload[agentv1.CenterHello](firstHello)
	if err != nil {
		t.Fatalf("decode first center hello: %v", err)
	}
	heartbeat, err := agentv1.NewEnvelope(agentv1.MessageAgentHeartbeat, agentv1.Heartbeat{
		SessionID: firstSession.SessionID, AgentVersion: "0.1.1", ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create heartbeat: %v", err)
	}
	if err := first.WriteJSON(heartbeat); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	ack := readTestAgentEnvelope(t, first)
	ackPayload, err := agentv1.DecodeEnvelopePayload[agentv1.HeartbeatAck](ack)
	if err != nil || ackPayload.MessageID != heartbeat.ID {
		t.Fatalf("heartbeat acknowledgement = %+v, %v", ackPayload, err)
	}
	if ackPayload.Command != nil {
		t.Fatal("heartbeat-only Agent received a command")
	}
	storedCommand, err := database.AgentCommandByID(context.Background(), heartbeatOnlyCommand.ID)
	if err != nil || storedCommand.Attempts != 0 {
		t.Fatalf("command behind capability gate = %+v, %v", storedCommand, err)
	}
	stored, err := database.NodeByID(context.Background(), node.ID)
	if err != nil || stored.LastSeenAt == nil || stored.AgentVersion != "0.1.1" {
		t.Fatalf("node after heartbeat = %+v, %v", stored, err)
	}

	second := connectTestAgent(t, server.URL, identity, "0.1.1")
	defer second.Close()
	secondHello := readTestAgentEnvelope(t, second)
	secondSession, err := agentv1.DecodeEnvelopePayload[agentv1.CenterHello](secondHello)
	if err != nil {
		t.Fatalf("decode second center hello: %v", err)
	}
	online := performRequest(handler, http.MethodGet, "/api/v1/nodes/"+node.ID, nil, nil, sessionCookie)
	var onlineNode nodeResponse
	decodeResponse(t, online, &onlineNode)
	if onlineNode.AgentStatus != "online" || onlineNode.AgentVersion != "0.1.1" || onlineNode.Hostname != "edge-one" {
		t.Fatalf("online node response = %+v", onlineNode)
	}
	if err := first.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set superseded session deadline: %v", err)
	}
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("superseded Agent session remained open")
	}
	disableBody := []byte(`{"name":"Edge","public_address":"","enabled":false}`)
	disabled := performRequest(handler, http.MethodPut, "/api/v1/nodes/"+node.ID, disableBody, headers, sessionCookie)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable node status = %d, body = %s", disabled.Code, disabled.Body.String())
	}
	var disabledNode nodeResponse
	decodeResponse(t, disabled, &disabledNode)
	if disabledNode.AgentStatus != "disabled" {
		t.Fatalf("disabled node response = %+v", disabledNode)
	}
	disabledHeartbeat, err := agentv1.NewEnvelope(agentv1.MessageAgentHeartbeat, agentv1.Heartbeat{
		SessionID: secondSession.SessionID, AgentVersion: "0.1.1", ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create disabled node heartbeat: %v", err)
	}
	if err := second.WriteJSON(disabledHeartbeat); err != nil {
		t.Fatalf("write disabled node heartbeat: %v", err)
	}
	disabledAck := readTestAgentEnvelope(t, second)
	disabledAckPayload, err := agentv1.DecodeEnvelopePayload[agentv1.HeartbeatAck](disabledAck)
	if err != nil || disabledAckPayload.MessageID != disabledHeartbeat.ID {
		t.Fatalf("disabled node heartbeat acknowledgement = %+v, %v", disabledAckPayload, err)
	}

	withoutCSRF := performRequest(handler, http.MethodDelete, "/api/v1/nodes/"+node.ID+"/agent-credential", nil, nil, sessionCookie)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("credential revocation without CSRF status = %d", withoutCSRF.Code)
	}
	revoke := performRequest(handler, http.MethodDelete, "/api/v1/nodes/"+node.ID+"/agent-credential", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if revoke.Code != http.StatusOK {
		t.Fatalf("credential revocation status = %d, body = %s", revoke.Code, revoke.Body.String())
	}
	var revokedNode nodeResponse
	decodeResponse(t, revoke, &revokedNode)
	if revokedNode.RegisteredAt != nil || revokedNode.AgentVersion != "" || len(revokedNode.Capabilities) != 0 {
		t.Fatalf("revoked node response = %+v", revokedNode)
	}
	if err := second.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set revoked session deadline: %v", err)
	}
	if _, _, err := second.ReadMessage(); err == nil {
		t.Fatal("revoked Agent session remained open")
	}
	if _, err := database.AuthenticateAgent(context.Background(), node.ID, auth.TokenHash(identity.Credential)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AuthenticateAgent() revoked credential error = %v", err)
	}
	secondRevoke := performRequest(handler, http.MethodDelete, "/api/v1/nodes/"+node.ID+"/agent-credential", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if secondRevoke.Code != http.StatusConflict {
		t.Fatalf("second credential revocation status = %d, body = %s", secondRevoke.Code, secondRevoke.Body.String())
	}

	enableBody := []byte(`{"name":"Edge","public_address":"","enabled":true}`)
	enabled := performRequest(handler, http.MethodPut, "/api/v1/nodes/"+node.ID, enableBody, headers, sessionCookie)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable revoked node status = %d, body = %s", enabled.Code, enabled.Body.String())
	}
	var enabledNode nodeResponse
	decodeResponse(t, enabled, &enabledNode)
	if enabledNode.AgentStatus != "pending" {
		t.Fatalf("enabled revoked node response = %+v", enabledNode)
	}
	reregisterTokenRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+node.ID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	var reregisterToken struct {
		Value string `json:"token"`
	}
	decodeResponse(t, reregisterTokenRequest, &reregisterToken)
	reregisterBody, err := json.Marshal(agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: reregisterToken.Value, AgentVersion: "0.1.2",
		Hostname: "edge-one", OS: "linux", Arch: "amd64", Capabilities: []string{"control.heartbeat"},
	})
	if err != nil {
		t.Fatalf("marshal Agent reregistration request: %v", err)
	}
	reregister := performRequest(handler, http.MethodPost, "/api/v1/agent/register", reregisterBody,
		map[string]string{"Content-Type": "application/json"})
	if reregister.Code != http.StatusCreated {
		t.Fatalf("Agent reregistration status = %d, body = %s", reregister.Code, reregister.Body.String())
	}
	audit := performRequest(handler, http.MethodGet, "/api/v1/audit", nil, nil, sessionCookie)
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), "node.credential.revoke") ||
		!strings.Contains(audit.Body.String(), "node.reregister") {
		t.Fatalf("credential lifecycle audit status = %d, body = %s", audit.Code, audit.Body.String())
	}
}

func TestAgentCommandDispatchResultAndReplay(t *testing.T) {
	handler, database := newTestHandler(t)
	identity, nodeID := registerTestAgent(t, handler, []string{
		agentv1.CapabilityControlCommands,
		agentv1.CapabilityControlHeartbeat,
	})
	now := time.Now().UTC()
	request := agentv1.Command{
		Kind: "agent.test", IssuedAt: now, ExpiresAt: now.Add(time.Hour), Payload: json.RawMessage(`{"value":1}`),
	}
	command, err := database.CreateAgentCommand(context.Background(), "command-1", nodeID, request, now)
	if err != nil {
		t.Fatalf("CreateAgentCommand() error = %v", err)
	}

	testServer := httptest.NewServer(handler)
	defer testServer.Close()
	connection := connectTestAgentWithCapabilities(t, testServer.URL, identity, "0.1.0", []string{
		agentv1.CapabilityControlCommands,
		agentv1.CapabilityControlHeartbeat,
	})
	defer connection.Close()
	centerHello := readTestAgentEnvelope(t, connection)
	session, err := agentv1.DecodeEnvelopePayload[agentv1.CenterHello](centerHello)
	if err != nil {
		t.Fatalf("decode center hello: %v", err)
	}

	heartbeat, err := agentv1.NewEnvelope(agentv1.MessageAgentHeartbeat, agentv1.Heartbeat{
		SessionID: session.SessionID, AgentVersion: "0.1.0", ObservedAt: time.Now().UTC(),
	})
	if err != nil || connection.WriteJSON(heartbeat) != nil {
		t.Fatalf("send heartbeat: %v", err)
	}
	heartbeatAck := readTestAgentEnvelope(t, connection)
	ackPayload, err := agentv1.DecodeEnvelopePayload[agentv1.HeartbeatAck](heartbeatAck)
	if err != nil || ackPayload.Command == nil {
		t.Fatalf("heartbeat acknowledgement = %+v, %v", ackPayload, err)
	}
	dispatched, err := agentv1.DecodeEnvelopePayload[agentv1.Command](*ackPayload.Command)
	if err != nil || ackPayload.Command.IdempotencyKey != command.ID {
		t.Fatalf("dispatched command = %+v, payload = %+v, %v", ackPayload.Command, dispatched, err)
	}
	digest, err := agentv1.CommandDigest(dispatched)
	if err != nil || digest != command.RequestSHA256 {
		t.Fatalf("dispatched command digest = %q, %v", digest, err)
	}

	resultEnvelope, err := agentv1.NewCommandResultEnvelope(agentv1.CommandResult{
		CommandID: command.ID, RequestSHA256: command.RequestSHA256, Status: agentv1.CommandStatusSucceeded,
		CompletedAt: time.Now().UTC(), Output: json.RawMessage(`{"applied":true}`),
	})
	if err != nil || connection.WriteJSON(resultEnvelope) != nil {
		t.Fatalf("send command result: %v", err)
	}
	resultAck := readTestAgentEnvelope(t, connection)
	if resultAck.Type != agentv1.MessageCenterCommandResultAck || resultAck.CorrelationID != resultEnvelope.ID {
		t.Fatalf("command result acknowledgement = %+v", resultAck)
	}
	if err := connection.WriteJSON(resultEnvelope); err != nil {
		t.Fatalf("replay command result: %v", err)
	}
	replayAck := readTestAgentEnvelope(t, connection)
	if replayAck.Type != agentv1.MessageCenterCommandResultAck || replayAck.CorrelationID != resultEnvelope.ID {
		t.Fatalf("replayed result acknowledgement = %+v", replayAck)
	}

	stored, err := database.AgentCommandByID(context.Background(), command.ID)
	if err != nil || stored.Status != store.AgentCommandSucceeded || stored.Attempts != 1 || stored.Result == nil {
		t.Fatalf("stored command = %+v, %v", stored, err)
	}
	secondHeartbeat, _ := agentv1.NewEnvelope(agentv1.MessageAgentHeartbeat, agentv1.Heartbeat{
		SessionID: session.SessionID, AgentVersion: "0.1.0", ObservedAt: time.Now().UTC(),
	})
	if err := connection.WriteJSON(secondHeartbeat); err != nil {
		t.Fatalf("send second heartbeat: %v", err)
	}
	secondAck := readTestAgentEnvelope(t, connection)
	secondPayload, err := agentv1.DecodeEnvelopePayload[agentv1.HeartbeatAck](secondAck)
	if err != nil || secondPayload.Command != nil {
		t.Fatalf("second heartbeat acknowledgement = %+v, %v", secondPayload, err)
	}
}

func TestAgentCommandCapabilityGate(t *testing.T) {
	if hasAgentCapability([]string{agentv1.CapabilityControlHeartbeat}, agentv1.CapabilityControlCommands) {
		t.Fatal("heartbeat-only Agent was treated as command capable")
	}
	if !hasAgentCapability([]string{agentv1.CapabilityControlCommands}, agentv1.CapabilityControlCommands) {
		t.Fatal("command-capable Agent was rejected")
	}
}

func connectTestAgent(t *testing.T, serverURL string, identity agentv1.RegisterResponse, version string) *websocket.Conn {
	return connectTestAgentWithCapabilities(t, serverURL, identity, version, []string{agentv1.CapabilityControlHeartbeat})
}

func connectTestAgentWithCapabilities(t *testing.T, serverURL string, identity agentv1.RegisterResponse, version string, capabilities []string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/v1/agent/connect/" + identity.NodeID
	headers := http.Header{"Authorization": []string{"Bearer " + identity.Credential}}
	connection, response, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("connect Agent status = %d: %v", response.StatusCode, err)
		}
		t.Fatalf("connect Agent: %v", err)
	}
	hello, err := agentv1.NewEnvelope(agentv1.MessageAgentHello, agentv1.AgentHello{
		NodeID: identity.NodeID, AgentVersion: version, StartedAt: time.Now().UTC(),
		Capabilities: capabilities,
	})
	if err != nil {
		connection.Close()
		t.Fatalf("create Agent hello: %v", err)
	}
	if err := connection.WriteJSON(hello); err != nil {
		connection.Close()
		t.Fatalf("write Agent hello: %v", err)
	}
	return connection
}

func registerTestAgent(t *testing.T, handler http.Handler, capabilities []string) (agentv1.RegisterResponse, string) {
	t.Helper()
	sessionCookie, csrfCookie := setupCookies(t, handler)
	headers := map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}
	nodeRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes", []byte(`{"name":"Edge"}`), headers, sessionCookie)
	if nodeRequest.Code != http.StatusCreated {
		t.Fatalf("create node status = %d, body = %s", nodeRequest.Code, nodeRequest.Body.String())
	}
	var node nodeResponse
	decodeResponse(t, nodeRequest, &node)
	tokenRequest := performRequest(handler, http.MethodPost, "/api/v1/nodes/"+node.ID+"/registration-tokens", nil,
		map[string]string{"X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	var token struct {
		Value string `json:"token"`
	}
	decodeResponse(t, tokenRequest, &token)
	body, err := json.Marshal(agentv1.RegisterRequest{
		APIVersion: agentv1.APIVersion, Token: token.Value, AgentVersion: "0.1.0",
		Hostname: "edge-one", OS: "linux", Arch: "amd64", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("marshal registration request: %v", err)
	}
	registration := performRequest(handler, http.MethodPost, "/api/v1/agent/register", body,
		map[string]string{"Content-Type": "application/json"})
	if registration.Code != http.StatusCreated {
		t.Fatalf("Agent registration status = %d, body = %s", registration.Code, registration.Body.String())
	}
	var identity agentv1.RegisterResponse
	decodeResponse(t, registration, &identity)
	return identity, node.ID
}

func readTestAgentEnvelope(t *testing.T, connection *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set Agent read deadline: %v", err)
	}
	var value protocol.Envelope
	if err := connection.ReadJSON(&value); err != nil {
		t.Fatalf("read Agent envelope: %v", err)
	}
	if err := agentv1.ValidateEnvelope(value); err != nil {
		t.Fatalf("Agent envelope = %+v: %v", value, err)
	}
	return value
}

func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	handler, database, _ := newTestHandlerWithEventStore(t)
	return handler, database
}

func newTestHandlerWithEventStore(t *testing.T) (http.Handler, *store.Store, *eventstore.Store) {
	return newTestHandlerWithOptions(t, nil)
}

func newTestHandlerWithWebAssets(t *testing.T, assets fs.FS) http.Handler {
	handler, _, _ := newTestHandlerWithOptions(t, assets)
	return handler
}

func newTestHandlerWithOptions(t *testing.T, assets fs.FS) (http.Handler, *store.Store, *eventstore.Store) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	events, err := eventstore.Open(context.Background(), filepath.Join(directory, "events.db"))
	if err != nil {
		t.Fatalf("eventstore.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatalf("secretbox.Open() error = %v", err)
	}
	authentication, err := auth.NewService(database, secrets)
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Options{
		Version: "test", Store: database, EventStore: events, Auth: authentication,
		Management: management.NewService(database, secrets), Secrets: secrets, Logger: logger, WebAssets: assets,
	}), database, events
}

func performRequest(handler http.Handler, method, path string, body []byte, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
}

func cookieByName(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found", name)
	return nil
}

func setupCookies(t *testing.T, handler http.Handler) (*http.Cookie, *http.Cookie) {
	t.Helper()
	setup := performRequest(handler, http.MethodPost, "/api/v1/setup",
		[]byte(`{"username":"admin","password":"correct horse battery staple"}`),
		map[string]string{"Content-Type": "application/json"})
	if setup.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", setup.Code, setup.Body.String())
	}
	return cookieByName(t, setup.Result().Cookies(), sessionCookieName), cookieByName(t, setup.Result().Cookies(), csrfCookieName)
}

func testTOTPCode(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		t.Fatalf("decode TOTP secret: %v", err)
	}
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(now.UTC().Unix()/30))
	digest := hmac.New(sha1.New, key)
	_, _ = digest.Write(message[:])
	sum := digest.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
