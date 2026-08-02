package server

import (
	"encoding/json"
	"mime"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Relayward/relayward-sdk/manifest"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type pluginReleaseRequest struct {
	Repository          string   `json:"repository"`
	Version             string   `json:"version"`
	GitHubToken         string   `json:"github_token,omitempty"`
	ApprovedPermissions []string `json:"approved_permissions,omitempty"`
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

func (server *Server) servePluginUI(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	name := request.PathValue("path")
	file, info, err := server.management.OpenPluginUIFile(request.Context(), request.PathValue("plugin_id"), name)
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
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
