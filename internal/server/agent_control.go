package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/Relayward/relayward-sdk/protocol"
)

const agentHandshakeTimeout = 20 * time.Second

type agentSession struct {
	id         string
	connection *websocket.Conn
}

type agentSessionHub struct {
	mu       sync.Mutex
	sessions map[string]agentSession
}

func newAgentSessionHub() *agentSessionHub {
	return &agentSessionHub{sessions: make(map[string]agentSession)}
}

func (hub *agentSessionHub) activate(nodeID, sessionID string, connection *websocket.Conn) {
	hub.mu.Lock()
	previous, exists := hub.sessions[nodeID]
	hub.sessions[nodeID] = agentSession{id: sessionID, connection: connection}
	hub.mu.Unlock()
	if exists {
		_ = previous.connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "superseded session"), time.Now().Add(time.Second))
		_ = previous.connection.Close()
	}
}

func (hub *agentSessionHub) deactivate(nodeID, sessionID string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if current, exists := hub.sessions[nodeID]; exists && current.id == sessionID {
		delete(hub.sessions, nodeID)
	}
}

func (hub *agentSessionHub) connected(nodeID string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	_, exists := hub.sessions[nodeID]
	return exists
}

func (hub *agentSessionHub) withActive(nodeID, sessionID string, operation func() error) error {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current, exists := hub.sessions[nodeID]
	if !exists || current.id != sessionID {
		return errSupersededAgentSession
	}
	return operation()
}

func (hub *agentSessionHub) disconnect(nodeID string) {
	hub.mu.Lock()
	current, exists := hub.sessions[nodeID]
	if exists {
		delete(hub.sessions, nodeID)
	}
	hub.mu.Unlock()
	if exists {
		_ = current.connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "credentials replaced"), time.Now().Add(time.Second))
		_ = current.connection.Close()
	}
}

var errSupersededAgentSession = errors.New("Agent session was superseded")

func (server *Server) connectAgent(w http.ResponseWriter, request *http.Request) {
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
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(request *http.Request) bool {
			return request.Header.Get("Origin") == ""
		},
	}
	connection, err := upgrader.Upgrade(w, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(agentv1.MaximumMessageBytes)

	if err := connection.SetReadDeadline(time.Now().Add(agentHandshakeTimeout)); err != nil {
		return
	}
	helloEnvelope, err := readAgentEnvelope(connection)
	if err != nil || helloEnvelope.Type != agentv1.MessageAgentHello {
		server.closeAgentProtocol(connection, protocol.ErrorInvalidArgument, "Invalid Agent hello.")
		return
	}
	hello, err := agentv1.DecodeEnvelopePayload[agentv1.AgentHello](helloEnvelope)
	if err != nil || hello.NodeID != node.ID {
		server.closeAgentProtocol(connection, protocol.ErrorUnauthenticated, "Agent hello does not match its identity.")
		return
	}
	if err := server.management.RecordAgentHello(request.Context(), node.ID, node.CredentialHash, hello, time.Now().UTC()); err != nil {
		server.closeAgentProtocol(connection, protocol.ErrorUnauthenticated, "Agent credentials are no longer valid.")
		return
	}
	sessionID, err := protocol.NewID()
	if err != nil {
		server.closeAgentProtocol(connection, protocol.ErrorInternal, "The control session could not be created.")
		return
	}
	server.agentSessions.activate(node.ID, sessionID, connection)
	defer server.agentSessions.deactivate(node.ID, sessionID)

	centerHello, err := agentv1.NewEnvelope(agentv1.MessageCenterHello, agentv1.CenterHello{
		SessionID: sessionID, HeartbeatIntervalSeconds: int(agentv1.DefaultHeartbeatInterval.Seconds()),
		ServerTime: time.Now().UTC(),
	})
	if err != nil || writeAgentEnvelope(connection, centerHello) != nil {
		return
	}

	for {
		if err := connection.SetReadDeadline(time.Now().Add(3 * agentv1.DefaultHeartbeatInterval)); err != nil {
			return
		}
		envelope, err := readAgentEnvelope(connection)
		if err != nil {
			return
		}
		if envelope.Type != agentv1.MessageAgentHeartbeat {
			server.closeAgentProtocol(connection, protocol.ErrorInvalidArgument, "Unexpected Agent message.")
			return
		}
		heartbeat, err := agentv1.DecodeEnvelopePayload[agentv1.Heartbeat](envelope)
		if err != nil || heartbeat.SessionID != sessionID {
			server.closeAgentProtocol(connection, protocol.ErrorInvalidArgument, "Invalid Agent heartbeat.")
			return
		}
		receivedAt := time.Now().UTC()
		err = server.agentSessions.withActive(node.ID, sessionID, func() error {
			return server.management.RecordAgentHeartbeat(request.Context(), node.ID, node.CredentialHash, heartbeat, receivedAt)
		})
		if err != nil {
			return
		}
		ack, err := agentv1.NewEnvelope(agentv1.MessageCenterHeartbeatAck, agentv1.HeartbeatAck{
			MessageID: envelope.ID, ServerTime: receivedAt,
		})
		if err != nil {
			return
		}
		ack.CorrelationID = envelope.ID
		if err := writeAgentEnvelope(connection, ack); err != nil {
			return
		}
	}
}

func readAgentEnvelope(connection *websocket.Conn) (protocol.Envelope, error) {
	messageType, raw, err := connection.ReadMessage()
	if err != nil {
		return protocol.Envelope{}, err
	}
	if messageType != websocket.TextMessage {
		return protocol.Envelope{}, fmt.Errorf("Agent message must be text JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value protocol.Envelope
	if err := decoder.Decode(&value); err != nil {
		return protocol.Envelope{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return protocol.Envelope{}, fmt.Errorf("Agent message contains trailing JSON")
	}
	if err := agentv1.ValidateEnvelope(value); err != nil {
		return protocol.Envelope{}, err
	}
	return value, nil
}

func writeAgentEnvelope(connection *websocket.Conn, value protocol.Envelope) error {
	if err := agentv1.ValidateEnvelope(value); err != nil {
		return err
	}
	if err := connection.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	return connection.WriteJSON(value)
}

func (server *Server) closeAgentProtocol(connection *websocket.Conn, code protocol.ErrorCode, message string) {
	value, err := agentv1.NewEnvelope(agentv1.MessageProtocolError, protocol.Problem{
		Code: code, Message: message, Retryable: false,
	})
	if err == nil {
		_ = writeAgentEnvelope(connection, value)
	}
	_ = connection.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "protocol error"), time.Now().Add(time.Second))
}
