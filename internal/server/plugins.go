package server

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

const pluginUIContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

const (
	pluginUISecretOwnerType = "plugin_ui"
	pluginUISecretName      = "asset_access"
)

type pluginReleaseRequest struct {
	Repository          string   `json:"repository"`
	Version             string   `json:"version"`
	GitHubToken         string   `json:"github_token,omitempty"`
	ApprovedPermissions []string `json:"approved_permissions,omitempty"`
}

type pluginReleaseListRequest struct {
	Repository  string `json:"repository"`
	GitHubToken string `json:"github_token,omitempty"`
}

type pluginUpgradeRequest struct {
	Version             string   `json:"version"`
	GitHubToken         string   `json:"github_token,omitempty"`
	ApprovedPermissions []string `json:"approved_permissions"`
}

type pluginGitHubTokenRequest struct {
	GitHubToken string `json:"github_token"`
}

type pluginReleaseCandidateResponse struct {
	Repository string            `json:"repository"`
	ReleaseID  int64             `json:"release_id"`
	Tag        string            `json:"tag"`
	Manifest   manifest.Manifest `json:"manifest"`
	Update     bool              `json:"update"`
}

type pluginReleaseVersionResponse struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
}

type pluginUISessionResponse struct {
	URL string `json:"url"`
}

type pluginUIAccess struct {
	Version   string    `json:"version"`
	ExpiresAt time.Time `json:"expires_at"`
}

type pluginInstallationResponse struct {
	PluginID            string            `json:"plugin_id"`
	Repository          string            `json:"repository"`
	Kind                string            `json:"kind"`
	DesiredVersion      string            `json:"desired_version"`
	ActiveVersion       string            `json:"active_version"`
	PreviousVersion     string            `json:"previous_version,omitempty"`
	Manifest            manifest.Manifest `json:"manifest"`
	ApprovedPermissions []string          `json:"approved_permissions"`
	ReleaseID           int64             `json:"release_id"`
	State               string            `json:"state"`
	Health              string            `json:"health"`
	RestartCount        uint64            `json:"restart_count"`
	LastProblem         *protocol.Problem `json:"last_problem,omitempty"`
	LastStartedAt       *time.Time        `json:"last_started_at,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

func (server *Server) inspectPluginRelease(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input pluginReleaseRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid plugin release request.", false)
		return
	}
	value, err := server.management.InspectPluginRelease(request.Context(), management.PluginReleaseInput{
		Repository: input.Repository, Version: input.Version, GitHubToken: input.GitHubToken,
	})
	if err != nil {
		server.resourceError(w, request, err, "Plugin release")
		return
	}
	writeJSON(w, http.StatusOK, pluginReleaseCandidateResponse{
		Repository: value.Repository, ReleaseID: value.ReleaseID, Tag: value.Tag,
		Manifest: value.Manifest, Update: value.Update,
	})
}

func (server *Server) listPluginReleaseVersions(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input pluginReleaseListRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid plugin release list request.", false)
		return
	}
	values, err := server.management.ListPluginReleaseVersions(
		request.Context(), input.Repository, input.GitHubToken,
	)
	if err != nil {
		server.resourceError(w, request, err, "Plugin releases")
		return
	}
	items := make([]pluginReleaseVersionResponse, len(values))
	for index, value := range values {
		items[index] = pluginReleaseVersionResponse{
			Tag: value.Tag, Version: value.Version, PublishedAt: value.PublishedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) listPluginInstallations(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListPluginInstallations(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]pluginInstallationResponse, len(values))
	for index, value := range values {
		items[index] = pluginInstallationView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) installPlugin(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input pluginReleaseRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid plugin installation request.", false)
		return
	}
	value, err := server.management.InstallPluginRelease(request.Context(), management.PluginReleaseInput{
		Repository: input.Repository, Version: input.Version, GitHubToken: input.GitHubToken,
		ApprovedPermissions: input.ApprovedPermissions,
	})
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	writeJSON(w, http.StatusCreated, pluginInstallationView(value))
}

func (server *Server) getPluginInstallation(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.PluginInstallation(request.Context(), request.PathValue("plugin_id"))
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	writeJSON(w, http.StatusOK, pluginInstallationView(value))
}

func (server *Server) upgradePlugin(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input pluginUpgradeRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid plugin upgrade request.", false)
		return
	}
	existing, err := server.management.PluginInstallation(request.Context(), request.PathValue("plugin_id"))
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	value, err := server.management.InstallPluginRelease(request.Context(), management.PluginReleaseInput{
		Repository: existing.Repository, Version: input.Version, GitHubToken: input.GitHubToken,
		ApprovedPermissions: input.ApprovedPermissions,
	})
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	writeJSON(w, http.StatusOK, pluginInstallationView(value))
}

func (server *Server) replacePluginGitHubToken(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input pluginGitHubTokenRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid GitHub token request.", false)
		return
	}
	if err := server.management.ReplacePluginGitHubToken(
		request.Context(), request.PathValue("plugin_id"), input.GitHubToken,
	); err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) uninstallPlugin(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	if err := server.management.UninstallPlugin(request.Context(), request.PathValue("plugin_id")); err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) invokePluginUI(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input json.RawMessage
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid plugin UI request.", false)
		return
	}
	value, err := server.management.InvokePluginUI(
		request.Context(), request.PathValue("plugin_id"), request.PathValue("method"), input,
	)
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(value)
}

func (server *Server) createPluginUISession(w http.ResponseWriter, request *http.Request, authenticated auth.Authenticated) {
	pluginID := request.PathValue("plugin_id")
	installation, err := server.management.PluginInstallation(request.Context(), pluginID)
	if err != nil {
		server.resourceError(w, request, err, "Plugin")
		return
	}
	if installation.Manifest.Requires.UIAPI == nil {
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "The plugin does not provide a user interface.", false)
		return
	}
	payload, err := json.Marshal(pluginUIAccess{
		Version: installation.ActiveVersion, ExpiresAt: authenticated.Session.ExpiresAt,
	})
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	ciphertext, err := server.secrets.Encrypt(pluginUISecretOwnerType, pluginID, pluginUISecretName, payload)
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(ciphertext)
	path := "/plugin-ui/" + url.PathEscape(pluginID) + "/" + token + "/index.html"
	writeJSON(w, http.StatusCreated, pluginUISessionResponse{URL: path})
}

func (server *Server) servePluginUI(w http.ResponseWriter, request *http.Request) {
	pluginID := request.PathValue("plugin_id")
	ciphertext, err := base64.RawURLEncoding.DecodeString(request.PathValue("token"))
	if err != nil || len(ciphertext) > 4096 {
		http.NotFound(w, request)
		return
	}
	payload, err := server.secrets.Decrypt(pluginUISecretOwnerType, pluginID, pluginUISecretName, ciphertext)
	if err != nil {
		http.NotFound(w, request)
		return
	}
	var access pluginUIAccess
	if err := json.Unmarshal(payload, &access); err != nil || access.Version == "" ||
		access.ExpiresAt.IsZero() || !time.Now().UTC().Before(access.ExpiresAt) {
		http.NotFound(w, request)
		return
	}
	installation, err := server.management.PluginInstallation(request.Context(), pluginID)
	if err != nil || installation.ActiveVersion != access.Version {
		http.NotFound(w, request)
		return
	}
	name := request.PathValue("path")
	file, info, err := server.management.OpenPluginUIFile(request.Context(), pluginID, name)
	if err != nil {
		server.resourceError(w, request, err, "Plugin UI asset")
		return
	}
	defer file.Close()
	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Security-Policy", pluginUIContentSecurityPolicy)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Del("X-Frame-Options")
	http.ServeContent(w, request, info.Name(), info.ModTime(), file)
}

func pluginInstallationView(value store.PluginInstallation) pluginInstallationResponse {
	permissions := value.ApprovedPermissions
	if permissions == nil {
		permissions = []string{}
	}
	return pluginInstallationResponse{
		PluginID: value.PluginID, Repository: value.Repository, Kind: value.Kind,
		DesiredVersion: value.DesiredVersion, ActiveVersion: value.ActiveVersion,
		PreviousVersion: value.PreviousVersion, Manifest: value.Manifest,
		ApprovedPermissions: permissions, ReleaseID: value.ReleaseID, State: value.State,
		Health: value.Health, RestartCount: value.RestartCount, LastProblem: value.LastProblem,
		LastStartedAt: value.LastStartedAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
