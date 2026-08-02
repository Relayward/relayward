package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const (
	sessionCookieName = "relayward_session"
	csrfCookieName    = "relayward_csrf"
	maxJSONBody       = 1 << 20
)

type Options struct {
	Version      string
	Store        *store.Store
	Auth         *auth.Service
	Management   *management.Service
	Secrets      *secretbox.Manager
	Logger       *slog.Logger
	SecureCookie bool
}

type Server struct {
	version      string
	store        *store.Store
	auth         *auth.Service
	management   *management.Service
	secrets      *secretbox.Manager
	logger       *slog.Logger
	secureCookie bool
	loginLimiter *attemptLimiter
}

type systemInfo struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	SecretsAvailable bool   `json:"secrets_available"`
}

func New(options Options) http.Handler {
	server := &Server{
		version:      options.Version,
		store:        options.Store,
		auth:         options.Auth,
		management:   options.Management,
		secrets:      options.Secrets,
		logger:       options.Logger,
		secureCookie: options.SecureCookie,
		loginLimiter: newAttemptLimiter(5, 5*time.Minute),
	}
	if server.logger == nil {
		server.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /api/v1/system/info", server.systemInfo)
	mux.HandleFunc("GET /api/v1/setup", server.setupStatus)
	mux.HandleFunc("POST /api/v1/setup", server.setup)
	mux.HandleFunc("POST /api/v1/auth/login", server.login)
	mux.HandleFunc("GET /api/v1/auth/session", server.withAuthentication(server.session))
	mux.HandleFunc("POST /api/v1/auth/logout", server.withAuthentication(server.withCSRF(server.logout)))
	mux.HandleFunc("POST /api/v1/auth/totp/prepare", server.withAuthentication(server.withCSRF(server.prepareTOTP)))
	mux.HandleFunc("POST /api/v1/auth/totp/enable", server.withAuthentication(server.withCSRF(server.enableTOTP)))
	mux.HandleFunc("POST /api/v1/auth/totp/disable", server.withAuthentication(server.withCSRF(server.disableTOTP)))
	mux.HandleFunc("POST /api/v1/auth/recovery-codes/regenerate", server.withAuthentication(server.withCSRF(server.regenerateRecoveryCodes)))
	mux.HandleFunc("GET /api/v1/nodes", server.withAuthentication(server.listNodes))
	mux.HandleFunc("POST /api/v1/nodes", server.withAuthentication(server.withCSRF(server.createNode)))
	mux.HandleFunc("GET /api/v1/nodes/{node_id}", server.withAuthentication(server.getNode))
	mux.HandleFunc("PUT /api/v1/nodes/{node_id}", server.withAuthentication(server.withCSRF(server.updateNode)))
	mux.HandleFunc("DELETE /api/v1/nodes/{node_id}", server.withAuthentication(server.withCSRF(server.deleteNode)))
	mux.HandleFunc("POST /api/v1/nodes/{node_id}/registration-tokens", server.withAuthentication(server.withCSRF(server.createNodeRegistrationToken)))
	mux.HandleFunc("GET /api/v1/users", server.withAuthentication(server.listUsers))
	mux.HandleFunc("POST /api/v1/users", server.withAuthentication(server.withCSRF(server.createUser)))
	mux.HandleFunc("GET /api/v1/users/{user_id}", server.withAuthentication(server.getUser))
	mux.HandleFunc("PUT /api/v1/users/{user_id}", server.withAuthentication(server.withCSRF(server.updateUser)))
	mux.HandleFunc("DELETE /api/v1/users/{user_id}", server.withAuthentication(server.withCSRF(server.deleteUser)))
	mux.HandleFunc("GET /api/v1/authorizations", server.withAuthentication(server.listAuthorizations))
	mux.HandleFunc("POST /api/v1/authorizations", server.withAuthentication(server.withCSRF(server.createAuthorization)))
	mux.HandleFunc("GET /api/v1/authorizations/{authorization_id}", server.withAuthentication(server.getAuthorization))
	mux.HandleFunc("PUT /api/v1/authorizations/{authorization_id}", server.withAuthentication(server.withCSRF(server.updateAuthorization)))
	mux.HandleFunc("DELETE /api/v1/authorizations/{authorization_id}", server.withAuthentication(server.withCSRF(server.deleteAuthorization)))
	mux.HandleFunc("POST /api/v1/authorizations/{authorization_id}/subscription-token", server.withAuthentication(server.withCSRF(server.rotateSubscriptionToken)))
	mux.HandleFunc("GET /api/v1/authorizations/{authorization_id}/service-bindings", server.withAuthentication(server.listServiceBindings))
	mux.HandleFunc("POST /api/v1/authorizations/{authorization_id}/service-bindings", server.withAuthentication(server.withCSRF(server.createServiceBinding)))
	mux.HandleFunc("PUT /api/v1/service-bindings/{binding_id}", server.withAuthentication(server.withCSRF(server.updateServiceBinding)))
	mux.HandleFunc("DELETE /api/v1/service-bindings/{binding_id}", server.withAuthentication(server.withCSRF(server.deleteServiceBinding)))
	mux.HandleFunc("GET /api/v1/audit", server.withAuthentication(server.listAudit))
	mux.HandleFunc("GET /api/v1/subscriptions/{subscription_token}", server.subscription)
	return server.securityHeaders(mux)
}

func (server *Server) health(w http.ResponseWriter, request *http.Request) {
	if err := server.store.Ping(request.Context()); err != nil {
		server.logger.Error("database health check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	status := "ok"
	if !server.secrets.Available() {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func (server *Server) systemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, systemInfo{Name: "relayward", Version: server.version, SecretsAvailable: server.secrets.Available()})
}

func (server *Server) setupStatus(w http.ResponseWriter, request *http.Request) {
	initialized, err := server.store.HasAdministrator(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"initialized":       initialized,
		"secrets_available": server.secrets.Available(),
	})
}

type setupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (server *Server) setup(w http.ResponseWriter, request *http.Request) {
	var input setupRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid setup request.", false)
		return
	}
	credentials, err := server.auth.Setup(request.Context(), input.Username, input.Password)
	if errors.Is(err, store.ErrAlreadyInitialized) {
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "Relayward is already initialized.", false)
		return
	}
	if err != nil {
		if errors.Is(err, auth.ErrInvalidPassword) || errors.Is(err, auth.ErrInvalidUsername) {
			writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, err.Error(), false)
			return
		}
		server.internalError(w, request, err)
		return
	}
	server.setCredentials(w, credentials)
	writeJSON(w, http.StatusCreated, sessionResponse(credentials.Admin.Username, credentials.Admin.TOTPEnabled, credentials.ExpiresAt, server.secrets.Available()))
}

type loginRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	SecondFactor string `json:"second_factor,omitempty"`
}

func (server *Server) login(w http.ResponseWriter, request *http.Request) {
	var input loginRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid login request.", false)
		return
	}
	limiterKey := remoteHost(request.RemoteAddr)
	if !server.loginLimiter.Allow(limiterKey, time.Now()) {
		writeProblem(w, http.StatusTooManyRequests, protocol.ErrorUnavailable, "Too many login attempts. Try again later.", true)
		return
	}
	credentials, err := server.auth.Login(request.Context(), input.Username, input.Password, input.SecondFactor)
	switch {
	case err == nil:
		server.loginLimiter.Reset(limiterKey)
		server.setCredentials(w, credentials)
		writeJSON(w, http.StatusOK, sessionResponse(credentials.Admin.Username, credentials.Admin.TOTPEnabled, credentials.ExpiresAt, server.secrets.Available()))
	case errors.Is(err, auth.ErrSecondFactorRequired):
		writeProblemWithViolations(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "A TOTP or recovery code is required.", false,
			[]protocol.FieldViolation{{Field: "second_factor", Description: "required"}})
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidSecondFactor):
		writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Invalid credentials.", false)
	case errors.Is(err, auth.ErrSecretsUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Encrypted secrets are unavailable. Use a recovery code or repair the instance key.", false)
	default:
		server.internalError(w, request, err)
	}
}

func (server *Server) session(w http.ResponseWriter, request *http.Request, authenticated auth.Authenticated) {
	writeJSON(w, http.StatusOK, sessionResponse(authenticated.Admin.Username, authenticated.Admin.TOTPEnabled, authenticated.Session.ExpiresAt, server.secrets.Available()))
}

func (server *Server) logout(w http.ResponseWriter, request *http.Request, authenticated auth.Authenticated) {
	if err := server.auth.Logout(request.Context(), authenticated); err != nil {
		server.internalError(w, request, err)
		return
	}
	server.clearCredentials(w)
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) prepareTOTP(w http.ResponseWriter, request *http.Request, authenticated auth.Authenticated) {
	secret, uri, err := server.auth.PrepareTOTP(request.Context(), authenticated)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
	case errors.Is(err, auth.ErrSecretsUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Encrypted secrets are unavailable.", false)
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "TOTP is already enabled.", false)
	case errors.Is(err, auth.ErrTOTPAlreadyEnabled):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "TOTP is already enabled.", false)
	default:
		server.internalError(w, request, err)
	}
}

type enableTOTPRequest struct {
	Code string `json:"code"`
}

func (server *Server) enableTOTP(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input enableTOTPRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid TOTP request.", false)
		return
	}
	codes, err := server.auth.EnableTOTP(request.Context(), input.Code)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
	case errors.Is(err, auth.ErrInvalidSecondFactor):
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid TOTP code.", false)
	case errors.Is(err, auth.ErrSecretsUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Encrypted secrets are unavailable.", false)
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "TOTP setup has not been prepared.", false)
	default:
		server.internalError(w, request, err)
	}
}

type sensitiveActionRequest struct {
	Password     string `json:"password"`
	SecondFactor string `json:"second_factor"`
}

func (server *Server) disableTOTP(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input sensitiveActionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid TOTP request.", false)
		return
	}
	if err := server.auth.DisableTOTP(request.Context(), input.Password, input.SecondFactor); err != nil {
		server.sensitiveActionError(w, request, err)
		return
	}
	server.clearCredentials(w)
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) regenerateRecoveryCodes(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input sensitiveActionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid recovery code request.", false)
		return
	}
	codes, err := server.auth.RegenerateRecoveryCodes(request.Context(), input.Password, input.SecondFactor)
	if err != nil {
		server.sensitiveActionError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (server *Server) sensitiveActionError(w http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrInvalidSecondFactor), errors.Is(err, auth.ErrSecondFactorRequired):
		writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Invalid credentials.", false)
	case errors.Is(err, auth.ErrSecretsUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, protocol.ErrorUnavailable, "Encrypted secrets are unavailable. A recovery code remains usable.", false)
	default:
		server.internalError(w, request, err)
	}
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, auth.Authenticated)

func (server *Server) withAuthentication(next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Authentication required.", false)
			return
		}
		authenticated, err := server.auth.Authenticate(request.Context(), cookie.Value)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			server.clearCredentials(w)
			writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Authentication required.", false)
			return
		}
		if err != nil {
			server.internalError(w, request, err)
			return
		}
		next(w, request, authenticated)
	}
}

func (server *Server) withCSRF(next authenticatedHandler) authenticatedHandler {
	return func(w http.ResponseWriter, request *http.Request, authenticated auth.Authenticated) {
		if !server.auth.ValidateCSRF(authenticated, request.Header.Get("X-CSRF-Token")) {
			writeProblem(w, http.StatusForbidden, protocol.ErrorPermissionDenied, "CSRF validation failed.", false)
			return
		}
		next(w, request, authenticated)
	}
}

func (server *Server) setCredentials(w http.ResponseWriter, credentials auth.Credentials) {
	maxAge := int(time.Until(credentials.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: credentials.SessionToken, Path: "/", MaxAge: maxAge,
		Expires: credentials.ExpiresAt, HttpOnly: true, Secure: server.secureCookie, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: credentials.CSRFToken, Path: "/", MaxAge: maxAge,
		Expires: credentials.ExpiresAt, HttpOnly: false, Secure: server.secureCookie, SameSite: http.SameSiteStrictMode})
}

func (server *Server) clearCredentials(w http.ResponseWriter) {
	expires := time.Unix(1, 0)
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, Expires: expires,
		HttpOnly: true, Secure: server.secureCookie, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Path: "/", MaxAge: -1, Expires: expires,
		HttpOnly: false, Secure: server.secureCookie, SameSite: http.SameSiteStrictMode})
}

func (server *Server) internalError(w http.ResponseWriter, request *http.Request, err error) {
	server.logger.Error("request failed", "method", request.Method, "route", request.Pattern, "error", err)
	writeProblem(w, http.StatusInternalServerError, protocol.ErrorInternal, "The request could not be completed.", false)
}

func (server *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, request)
	})
}

func decodeJSON(request *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("content type must be application/json")
	}
	request.Body = http.MaxBytesReader(nil, request.Body, maxJSONBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func remoteHost(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func sessionResponse(username string, totpEnabled bool, expiresAt time.Time, secretsAvailable bool) map[string]any {
	return map[string]any{
		"administrator":     map[string]any{"username": username, "totp_enabled": totpEnabled},
		"expires_at":        expiresAt.UTC().Format(time.RFC3339),
		"secrets_available": secretsAvailable,
	}
}

func writeProblem(w http.ResponseWriter, status int, code protocol.ErrorCode, message string, retryable bool) {
	writeProblemWithViolations(w, status, code, message, retryable, nil)
}

func writeProblemWithViolations(w http.ResponseWriter, status int, code protocol.ErrorCode, message string, retryable bool, violations []protocol.FieldViolation) {
	writeJSON(w, status, protocol.Problem{Code: code, Message: message, Retryable: retryable, Violations: violations})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
