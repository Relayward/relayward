package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
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
	handler, _ := newTestHandler(t)
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
			if got := subscriptionStatus(test.snapshot, now); got != test.want {
				t.Fatalf("subscriptionStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func newTestHandler(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatalf("secretbox.Open() error = %v", err)
	}
	authentication, err := auth.NewService(database, secrets)
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Options{Version: "test", Store: database, Auth: authentication, Management: management.NewService(database), Secrets: secrets, Logger: logger}), database
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
