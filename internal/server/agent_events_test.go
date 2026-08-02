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

	"github.com/Relayward/relayward/internal/store"
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

func TestAgentPluginStatusEventsUpdateInstancesAndDoNotBlockOnUnknownPlugins(t *testing.T) {
	handler, database, events := newTestHandlerWithEventStore(t)
	sessionCookie, csrfCookie := setupCookies(t, handler)
	node := createPluginNode(t, handler, sessionCookie, csrfCookie)
	identity := registerPluginAgent(t, handler, node.ID, sessionCookie, csrfCookie)
	pluginManifest := serverRuntimeManifest()
	now := time.Now().UTC()
	if err := database.CreatePluginInstallation(t.Context(), store.PluginInstallation{
		PluginID: pluginManifest.ID, Repository: "https://github.com/Relayward/test-plugin",
		Kind: string(pluginManifest.Kind), DesiredVersion: pluginManifest.Version,
		ActiveVersion: pluginManifest.Version, Manifest: pluginManifest, State: "active",
	}, now); err != nil {
		t.Fatalf("CreatePluginInstallation() error = %v", err)
	}
	pluginPath := "/api/v1/nodes/" + node.ID + "/plugins/" + pluginManifest.ID
	queued := performRequest(handler, http.MethodPut, pluginPath,
		[]byte(`{"desired_state":"running","version":"1.2.3","configuration":{"enabled":true}}`),
		map[string]string{"Content-Type": "application/json", "X-CSRF-Token": csrfCookie.Value}, sessionCookie)
	if queued.Code != http.StatusOK {
		t.Fatalf("queue plugin status = %d, body = %s", queued.Code, queued.Body.String())
	}
	var instance nodePluginResponse
	decodeResponse(t, queued, &instance)
	status := agentv1.PluginStatusEvent{
		PluginID: pluginManifest.ID, Generation: instance.Generation, State: agentv1.PluginStateRunning,
		Version: pluginManifest.Version, ConfigurationSHA256: instance.DesiredConfigurationSHA256,
		Health: agentv1.PluginHealthHealthy, RestartCount: 2,
	}
	observedAt := now.Add(time.Minute)
	event, err := agentv1.NewEvent(identity.NodeID, testEventStreamID, 1, agentv1.EventPluginStatus, observedAt, status)
	if err != nil {
		t.Fatalf("NewEvent() plugin status error = %v", err)
	}
	batch := agentv1.EventBatch{
		APIVersion: agentv1.APIVersion, NodeID: identity.NodeID, StreamID: testEventStreamID,
		FirstSequence: 1, LastSequence: 1, Events: []agentv1.Event{event},
	}
	accepted := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID,
		gzipJSON(t, batch, gzip.BestSpeed), eventUploadHeaders(identity.Credential))
	if accepted.Code != http.StatusOK {
		t.Fatalf("plugin status upload = %d, body = %s", accepted.Code, accepted.Body.String())
	}
	actual, err := database.NodePluginInstanceByID(t.Context(), node.ID, pluginManifest.ID)
	if err != nil || actual.ActualState != agentv1.PluginStateRunning || actual.RestartCount != 2 || actual.Health != agentv1.PluginHealthHealthy {
		t.Fatalf("node plugin after status = %+v, %v", actual, err)
	}

	unknown := status
	unknown.PluginID = "io.relayward.deleted"
	unknownEvent, err := agentv1.NewEvent(identity.NodeID, testEventStreamID, 2, agentv1.EventPluginStatus, observedAt.Add(time.Second), unknown)
	if err != nil {
		t.Fatalf("NewEvent() unknown plugin status error = %v", err)
	}
	batch.FirstSequence, batch.LastSequence, batch.Events = 2, 2, []agentv1.Event{unknownEvent}
	ignored := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID,
		gzipJSON(t, batch, gzip.BestSpeed), eventUploadHeaders(identity.Credential))
	if ignored.Code != http.StatusOK {
		t.Fatalf("unknown plugin status upload = %d, body = %s", ignored.Code, ignored.Body.String())
	}

	invalidEvent, err := agentv1.NewEvent(identity.NodeID, testEventStreamID, 3, agentv1.EventPluginStatus,
		observedAt.Add(2*time.Second), map[string]any{"plugin_id": pluginManifest.ID})
	if err != nil {
		t.Fatalf("NewEvent() invalid plugin status error = %v", err)
	}
	batch.FirstSequence, batch.LastSequence, batch.Events = 3, 3, []agentv1.Event{invalidEvent}
	invalid := performRequest(handler, http.MethodPost, "/api/v1/agent/events/"+identity.NodeID,
		gzipJSON(t, batch, gzip.BestSpeed), eventUploadHeaders(identity.Credential))
	assertProblem(t, invalid, http.StatusBadRequest, protocol.ErrorInvalidArgument)
	count, err := events.Count(t.Context())
	if err != nil || count != 2 {
		t.Fatalf("event count after plugin statuses = %d, %v", count, err)
	}
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
