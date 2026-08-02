package server

import (
	"errors"
	"net/http"
	"strings"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/store"
)

func (server *Server) registerAgent(w http.ResponseWriter, request *http.Request) {
	var input agentv1.RegisterRequest
	if err := decodeJSON(request, &input); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid Agent registration request.", false)
		return
	}
	registered, err := server.management.RegisterAgent(request.Context(), input)
	if errors.Is(err, store.ErrNotFound) {
		writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Invalid or expired registration token.", false)
		return
	}
	if err != nil {
		server.resourceError(w, request, err, "Agent registration")
		return
	}
	server.agentSessions.disconnect(registered.Node.ID)
	writeJSON(w, http.StatusCreated, agentv1.RegisterResponse{
		APIVersion: agentv1.APIVersion,
		NodeID:     registered.Node.ID,
		NodeName:   registered.Node.Name,
		Credential: registered.Credential,
	})
}

func bearerCredential(request *http.Request) (string, bool) {
	value := request.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) || strings.ContainsAny(value[len(prefix):], " \t\r\n") {
		return "", false
	}
	credential := value[len(prefix):]
	return credential, credential != ""
}
