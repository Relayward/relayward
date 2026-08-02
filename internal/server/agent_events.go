package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/eventstore"
)

func (server *Server) receiveAgentEvents(w http.ResponseWriter, request *http.Request) {
	nodeID := request.PathValue("node_id")
	credential, ok := bearerCredential(request)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Agent authentication required.", false)
		return
	}
	node, err := server.management.AuthenticateAgent(request.Context(), nodeID, credential)
	if err != nil {
		writeProblem(w, http.StatusUnauthorized, protocol.ErrorUnauthenticated, "Invalid Agent credentials.", false)
		return
	}
	if !hasAgentCapability(node.Capabilities, agentv1.CapabilityEventQueue) {
		writeProblem(w, http.StatusConflict, protocol.ErrorUnsupported, "Agent does not advertise event queue support.", false)
		return
	}
	batch, err := decodeAgentEventBatch(w, request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid Agent event batch.", false)
		return
	}
	if err := agentv1.ValidateEventBatchForNode(node.ID, batch); err != nil {
		writeProblem(w, http.StatusBadRequest, protocol.ErrorInvalidArgument, "Invalid Agent event batch.", false)
		return
	}
	if server.eventStore == nil {
		server.internalError(w, request, errors.New("event store is not configured"))
		return
	}
	receivedAt := time.Now().UTC()
	highest, err := server.eventStore.Ingest(request.Context(), node.ID, batch, receivedAt)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, agentv1.EventBatchAck{
			APIVersion: agentv1.APIVersion, StreamID: batch.StreamID,
			HighestContiguousSequence: highest, ServerTime: receivedAt,
		})
	case errors.Is(err, eventstore.ErrGap), errors.Is(err, eventstore.ErrConflict):
		writeProblem(w, http.StatusConflict, protocol.ErrorConflict, "Agent event batch conflicts with persisted sequence state.", false)
	default:
		server.internalError(w, request, err)
	}
}

func decodeAgentEventBatch(w http.ResponseWriter, request *http.Request) (agentv1.EventBatch, error) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return agentv1.EventBatch{}, errors.New("content type must be application/json")
	}
	if !strings.EqualFold(strings.TrimSpace(request.Header.Get("Content-Encoding")), "gzip") {
		return agentv1.EventBatch{}, errors.New("content encoding must be gzip")
	}
	request.Body = http.MaxBytesReader(w, request.Body, agentv1.MaximumEventBatchCompressedBytes)
	compressed, err := gzip.NewReader(request.Body)
	if err != nil {
		return agentv1.EventBatch{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(compressed, agentv1.MaximumEventBatchExpandedBytes+1))
	closeErr := compressed.Close()
	if readErr != nil {
		return agentv1.EventBatch{}, readErr
	}
	if closeErr != nil {
		return agentv1.EventBatch{}, closeErr
	}
	if len(raw) > agentv1.MaximumEventBatchExpandedBytes {
		return agentv1.EventBatch{}, errors.New("expanded event batch exceeds limit")
	}
	return agentv1.DecodeEventBatch(bytes.NewReader(raw))
}
