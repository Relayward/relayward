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
	return New(Options{Version: "test", Store: database, Auth: authentication, Secrets: secrets, Logger: logger}), database
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
