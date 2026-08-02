package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

const testEventStreamID = "0123456789abcdef0123456789abcdef"

func TestAgentEventUploadPersistsBeforeAcknowledgementAndReplays(t *testing.T) {
	handler, _, events := newTestHandlerWithEventStore(t)
	identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
	batch := testAgentEventBatch(t, identity.NodeID, 1, 2, "original")
	body := gzipJSON(t, batch, gzip.BestSpeed)
	headers := eventUploadHeaders(identity.Credential)

	response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID, body, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("event upload status = %d, body = %s", response.Code, response.Body.String())
	}
	var acknowledgement agentv1.EventBatchAck
	decodeResponse(t, response, &acknowledgement)
	if acknowledgement.StreamID != batch.StreamID || acknowledgement.HighestContiguousSequence != 2 {
		t.Fatalf("event acknowledgement = %+v", acknowledgement)
	}
	stored, err := events.EventByID(t.Context(), batch.Events[0].EventID)
	if err != nil || stored.NodeID != identity.NodeID || stored.Event.EventID != batch.Events[0].EventID {
		t.Fatalf("persisted event = %+v, %v", stored, err)
	}

	replay := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID, body, headers)
	if replay.Code != http.StatusOK {
		t.Fatalf("replayed event upload status = %d, body = %s", replay.Code, replay.Body.String())
	}
	count, err := events.Count(t.Context())
	if err != nil || count != 2 {
		t.Fatalf("event count after replay = %d, %v", count, err)
	}
}

func TestAgentEventUploadAuthenticationCapabilityAndNodeBinding(t *testing.T) {
	t.Run("authentication required", func(t *testing.T) {
		handler, _, _ := newTestHandlerWithEventStore(t)
		identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
		body := gzipJSON(t, testAgentEventBatch(t, identity.NodeID, 1, 1, "auth"), gzip.BestSpeed)
		response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID, body,
			map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
		assertProblem(t, response, http.StatusUnauthorized, protocol.ErrorUnauthenticated)
	})

	t.Run("capability required", func(t *testing.T) {
		handler, _, _ := newTestHandlerWithEventStore(t)
		identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityControlHeartbeat})
		body := gzipJSON(t, testAgentEventBatch(t, identity.NodeID, 1, 1, "capability"), gzip.BestSpeed)
		response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID, body,
			eventUploadHeaders(identity.Credential))
		assertProblem(t, response, http.StatusConflict, protocol.ErrorUnsupported)
	})

	t.Run("batch node must match authenticated node", func(t *testing.T) {
		handler, _, _ := newTestHandlerWithEventStore(t)
		identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
		batch := testAgentEventBatch(t, "223e4567-e89b-42d3-a456-426614174000", 1, 1, "other-node")
		response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID,
			gzipJSON(t, batch, gzip.BestSpeed), eventUploadHeaders(identity.Credential))
		assertProblem(t, response, http.StatusBadRequest, protocol.ErrorInvalidArgument)
	})
}

func TestAgentEventUploadRejectsInvalidAndOversizedEncoding(t *testing.T) {
	handler, _, _ := newTestHandlerWithEventStore(t)
	identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
	batch := testAgentEventBatch(t, identity.NodeID, 1, 1, "encoding")
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal event batch: %v", err)
	}

	tests := []struct {
		name    string
		body    []byte
		headers map[string]string
	}{
		{name: "gzip required", body: raw, headers: map[string]string{
			"Authorization": "Bearer " + identity.Credential, "Content-Type": "application/json",
		}},
		{name: "JSON content type required", body: gzipBytes(t, raw, gzip.BestSpeed), headers: map[string]string{
			"Authorization": "Bearer " + identity.Credential, "Content-Type": "text/plain", "Content-Encoding": "gzip",
		}},
		{name: "corrupt gzip", body: []byte("not a gzip stream"), headers: eventUploadHeaders(identity.Credential)},
		{name: "expanded limit", body: gzipBytes(t,
			bytes.Repeat([]byte(" "), agentv1.MaximumEventBatchExpandedBytes+1), gzip.BestSpeed), headers: eventUploadHeaders(identity.Credential)},
		{name: "compressed limit", body: gzipBytes(t,
			bytes.Repeat([]byte("x"), agentv1.MaximumEventBatchCompressedBytes+1), gzip.NoCompression), headers: eventUploadHeaders(identity.Credential)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID, test.body, test.headers)
			assertProblem(t, response, http.StatusBadRequest, protocol.ErrorInvalidArgument)
		})
	}
}

func TestAgentEventUploadRejectsSequenceGapAndConflict(t *testing.T) {
	handler, _, events := newTestHandlerWithEventStore(t)
	identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
	endpoint := "/api/v1/agent/events/" + identity.NodeID
	headers := eventUploadHeaders(identity.Credential)

	gap := performRequest(handler, http.MethodPost, endpoint,
		gzipJSON(t, testAgentEventBatch(t, identity.NodeID, 2, 2, "gap"), gzip.BestSpeed), headers)
	assertProblem(t, gap, http.StatusConflict, protocol.ErrorConflict)

	original := testAgentEventBatch(t, identity.NodeID, 1, 1, "original")
	accepted := performRequest(handler, http.MethodPost, endpoint, gzipJSON(t, original, gzip.BestSpeed), headers)
	if accepted.Code != http.StatusOK {
		t.Fatalf("initial event status = %d, body = %s", accepted.Code, accepted.Body.String())
	}
	conflicting := testAgentEventBatch(t, identity.NodeID, 1, 1, "changed")
	conflict := performRequest(handler, http.MethodPost, endpoint, gzipJSON(t, conflicting, gzip.BestSpeed), headers)
	assertProblem(t, conflict, http.StatusConflict, protocol.ErrorConflict)
	count, err := events.Count(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("event count after conflicts = %d, %v", count, err)
	}
}

func TestAgentEventUploadDoesNotAcknowledgeStorageFailure(t *testing.T) {
	handler, _, events := newTestHandlerWithEventStore(t)
	identity, _ := registerTestAgent(t, handler, []string{agentv1.CapabilityEventQueue})
	if err := events.Close(); err != nil {
		t.Fatalf("close event store: %v", err)
	}
	batch := testAgentEventBatch(t, identity.NodeID, 1, 1, "storage-failure")
	response := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID,
		gzipJSON(t, batch, gzip.BestSpeed), eventUploadHeaders(identity.Credential))
	assertProblem(t, response, http.StatusInternalServerError, protocol.ErrorInternal)
}

func testAgentEventBatch(t *testing.T, nodeID string, first, last uint64, marker string) agentv1.EventBatch {
	t.Helper()
	events := make([]agentv1.Event, 0, last-first+1)
	observedAt := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	for sequence := first; sequence <= last; sequence++ {
		event, err := agentv1.NewEvent(nodeID, testEventStreamID, sequence, "system.test", observedAt.Add(time.Duration(sequence)*time.Millisecond),
			map[string]any{"marker": marker, "sequence": sequence})
		if err != nil {
			t.Fatalf("create event %d: %v", sequence, err)
		}
		events = append(events, event)
	}
	return agentv1.EventBatch{
		APIVersion: agentv1.APIVersion, NodeID: nodeID, StreamID: testEventStreamID,
		FirstSequence: first, LastSequence: last, Events: events,
	}
}

func gzipJSON(t *testing.T, value any, level int) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal gzip JSON: %v", err)
	}
	return gzipBytes(t, raw, level)
}

func gzipBytes(t *testing.T, raw []byte, level int) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, level)
	if err != nil {
		t.Fatalf("create gzip writer: %v", err)
	}
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("write gzip body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return compressed.Bytes()
}

func eventUploadHeaders(credential string) map[string]string {
	return map[string]string{
		"Authorization":    "Bearer " + credential,
		"Content-Type":     "application/json",
		"Content-Encoding": "gzip",
	}
}

func assertProblem(t *testing.T, response interface {
	Result() *http.Response
}, wantStatus int, wantCode protocol.ErrorCode) {
	t.Helper()
	result := response.Result()
	defer result.Body.Close()
	if result.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d", result.StatusCode, wantStatus)
	}
	var problem protocol.Problem
	if err := json.NewDecoder(result.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != wantCode || strings.TrimSpace(problem.Message) == "" {
		t.Fatalf("problem = %+v, want code %q", problem, wantCode)
	}
}
