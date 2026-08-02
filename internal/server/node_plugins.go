package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Relayward/relayward-sdk/protocol"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/store"
)

type nodePluginRequest struct {
	DesiredState  string          `json:"desired_state"`
	Version       string          `json:"version"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
}

type nodePluginResponse struct {
	NodeID                     string            `json:"node_id"`
	NodeName                   string            `json:"node_name"`
	PluginID                   string            `json:"plugin_id"`
	PluginName                 string            `json:"plugin_name"`
	DesiredVersion             string            `json:"desired_version"`
	ActiveVersion              string            `json:"active_version"`
	DesiredState               string            `json:"desired_state"`
	ActualState                string            `json:"actual_state"`
	Generation                 uint64            `json:"generation"`
	DesiredConfigurationSHA256 string            `json:"desired_configuration_sha256"`
	ArtifactSize               int64             `json:"artifact_size"`
	ArtifactSHA256             string            `json:"artifact_sha256"`
	ActualGeneration           uint64            `json:"actual_generation"`
	ActualConfigurationSHA256  string            `json:"actual_configuration_sha256"`
	Health                     string            `json:"health"`
	Reason                     string            `json:"reason"`
	RestartCount               uint64            `json:"restart_count"`
	ReconcileStatus            string            `json:"reconcile_status"`
	LastProblem                *protocol.Problem `json:"last_problem,omitempty"`
	LastCommandID              string            `json:"last_command_id"`
	CommandStatus              string            `json:"command_status"`
	CommandAttempts            int               `json:"command_attempts"`
	CommandLastSentAt          *time.Time        `json:"command_last_sent_at"`
	CommandCompletedAt         *time.Time        `json:"command_completed_at"`
	ActualObservedAt           *time.Time        `json:"actual_observed_at"`
	UpdatedAt                  time.Time         `json:"updated_at"`
}

func (server *Server) listNodePluginInstances(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	values, err := server.management.ListNodePluginInstances(request.Context())
	if err != nil {
		server.internalError(w, request, err)
		return
	}
	items := make([]nodePluginResponse, len(values))
	for index, value := range values {
		items[index] = nodePluginView(value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (server *Server) getNodePluginInstance(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	value, err := server.management.NodePluginInstance(
		request.Context(), request.PathValue("node_id"), request.PathValue("plugin_id"),
	)
	if err != nil {
		server.resourceError(w, request, err, "Node plugin instance")
		return
	}
	writeJSON(w, http.StatusOK, nodePluginView(value))
}

func (server *Server) reconcileNodePlugin(w http.ResponseWriter, request *http.Request, _ auth.Authenticated) {
	var input nodePluginRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid node plugin request.", false)
		return
	}
	value, err := server.management.ReconcileNodePlugin(request.Context(), request.PathValue("node_id"), request.PathValue("plugin_id"), management.NodePluginInput{
		DesiredState: input.DesiredState, Version: input.Version, Configuration: input.Configuration,
	})
	if err != nil {
		server.resourceError(w, request, err, "Node plugin instance")
		return
	}
	writeJSON(w, http.StatusOK, nodePluginView(value))
}

func nodePluginView(value store.NodePluginInstance) nodePluginResponse {
	commandStatus := value.CommandStatus
	if commandStatus == "" {
		commandStatus = "none"
	}
	return nodePluginResponse{
		NodeID: value.NodeID, NodeName: value.NodeName, PluginID: value.PluginID, PluginName: value.PluginName,
		DesiredVersion: value.DesiredVersion, ActiveVersion: value.ActiveVersion,
		DesiredState: value.DesiredState, ActualState: value.ActualState, Generation: value.Generation,
		DesiredConfigurationSHA256: value.DesiredConfigurationSHA256,
		ArtifactSize:               value.ArtifactSize, ArtifactSHA256: value.ArtifactSHA256,
		ActualGeneration: value.ActualGeneration, ActualConfigurationSHA256: value.ActualConfigurationSHA256,
		Health: value.Health, Reason: value.Reason, RestartCount: value.RestartCount,
		ReconcileStatus: value.ReconcileStatus, LastProblem: value.LastProblem,
		LastCommandID: value.LastCommandID, CommandStatus: commandStatus,
		CommandAttempts: value.CommandAttempts, CommandLastSentAt: value.CommandLastSentAt,
		CommandCompletedAt: value.CommandCompletedAt, ActualObservedAt: value.ActualObservedAt,
		UpdatedAt: value.UpdatedAt,
	}
}
