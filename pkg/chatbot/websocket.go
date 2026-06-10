package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"

	"github.com/gorilla/websocket"
)

// Default approval timeout
const DefaultApprovalTimeout = 5 * time.Minute

// WebSocket message types
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ApprovalRequest holds the approval ID and result channel
type ApprovalRequest struct {
	ApprovalID string
	ResultChan chan ApprovalResultMap
}

// WSSession represents a WebSocket session with its connection
type WSSession struct {
	conn        *websocket.Conn
	connMu      sync.Mutex
	cfg         *config.Config
	SessionID   string
	ChatName    string
	ChatSession *ChatSession
	ChatBot     *ChatBot
	WSHandler   *WSChatHandler

	// closed is set to true when the connection is closing, to prevent
	// writes to a closed connection from in-flight goroutines.
	closed atomic.Bool

	// readTimeout is used to reset the read deadline after a successful write.
	// This prevents SendMessage from starving SendPing to the point where
	// ReadMessage's pongWait expires.
	readTimeout time.Duration

	// Approval state for handling authorization requests
	approvalTimeout time.Duration
	pendingApproval *ApprovalRequest
	approvalMu      sync.Mutex

	// Cancel state for stopping ongoing chat
	cancelMu    sync.Mutex
	cancelFunc  context.CancelFunc
	isCancelled bool
}

func NewWSSession(conn *websocket.Conn, sessionID string, cfg *config.Config) *WSSession {
	session := &WSSession{
		conn:            conn,
		cfg:             cfg,
		SessionID:       sessionID,
		ChatName:        "",
		ChatSession:     nil,
		ChatBot:         nil,
		WSHandler:       nil,
		approvalTimeout: DefaultApprovalTimeout,
		pendingApproval: nil,
		isCancelled:     false,
	}
	return session
}

// MarkClosed marks the session as closed so that subsequent SendMessage/SendPing
// calls are silently dropped instead of writing to a closed connection.
func (s *WSSession) MarkClosed() {
	s.closed.Store(true)
}

// IsClosed returns true if the session has been marked as closed.
func (s *WSSession) IsClosed() bool {
	return s.closed.Load()
}

// SetCancelled marks the session as cancelled
func (s *WSSession) SetCancelled() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if !s.isCancelled {
		s.isCancelled = true
		if s.cancelFunc != nil {
			s.cancelFunc()
		}
	}
}

// IsCancelled returns true if the session is cancelled
func (s *WSSession) IsCancelled() bool {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.isCancelled
}

// ResetCancel resets the cancel state for a new request
func (s *WSSession) ResetCancel() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.isCancelled {
		s.isCancelled = false
		s.cancelFunc = nil
	}
}

// SetCancelFunc sets the cancel function for the current request
func (s *WSSession) SetCancelFunc(cancelFunc context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancelFunc = cancelFunc
}

func (s *WSSession) SendMessage(msgType string, content interface{}) {
	if s.IsClosed() {
		return
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	// Set write deadline to prevent blocking forever on slow clients.
	// Without this, a blocked SendMessage holds connMu, starving SendPing,
	// which causes pongWait to expire and the connection to be closed.
	s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer s.conn.SetWriteDeadline(time.Time{})
	data := WSMessage{Type: msgType}
	payload, _ := json.Marshal(content)
	data.Payload = payload
	if err := s.conn.WriteJSON(data); err != nil {
		log.Printf("Error sending message to session %s: %v", s.SessionID, err)
	}
	// Reset read deadline: a successful write proves the connection is alive,
	// so give ReadMessage more time. This prevents SendPing starvation from
	// causing a premature pongWait timeout.
	if s.readTimeout > 0 {
		s.conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	}
}

// SendPing sends a WebSocket ping frame to the client.
// Used for keepalive to detect dead connections (e.g., mobile network loss).
// The write deadline ensures we don't block forever if the connection is dead.
// The deadline is cleared after the write to avoid affecting subsequent writes.
func (s *WSSession) SendPing() {
	if s.IsClosed() {
		return
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	defer s.conn.SetWriteDeadline(time.Time{}) // Clear write deadline after ping
	if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
		log.Printf("Ping failed for session %s: %v", s.SessionID, err)
	}
}



func (s *WSSession) SendChunk(content string, isFirst, isLast bool, contentType string) {
	s.SendMessage("chunk", map[string]interface{}{
		"content":      content,
		"first":        isFirst,
		"last":         isLast,
		"content_type": contentType,
	})
}

func (s *WSSession) SendError(errMsg string) {
	s.SendMessage("error", map[string]string{"error": errMsg})
}

// HandleApprovalResponse processes an approval response from the client
// This method is called from the main read loop when an approval_response message is received
func (s *WSSession) HandleApprovalResponse(approvalID string, results ApprovalResultMap) {
	s.approvalMu.Lock()

	if s.pendingApproval == nil {
		s.approvalMu.Unlock()
		log.Printf("Session %s: No pending approval request for %s", s.SessionID, approvalID)
		return
	}

	if s.pendingApproval.ApprovalID != approvalID {
		s.approvalMu.Unlock()
		log.Printf("Session %s: Ignoring stale approval response (expected %s, got %s)",
			s.SessionID, s.pendingApproval.ApprovalID, approvalID)
		return
	}

	log.Printf("Session %s: Received approval response for %s with %d results", s.SessionID, approvalID, len(results))

	// Capture the channel reference before clearing pendingApproval
	resultChan := s.pendingApproval.ResultChan

	// Clear pending approval BEFORE sending to avoid race with timeout
	s.pendingApproval = nil
	s.approvalMu.Unlock()

	// Send result to waiting request using non-blocking send
	// This ensures we don't block the WebSocket read loop
	select {
	case resultChan <- results:
		log.Printf("Session %s: Approval result sent successfully for %s", s.SessionID, approvalID)
	default:
		// Channel might be full (timeout already fired) or closed
		// Log and silently ignore - the timeout handler will clean up
		log.Printf("Session %s: Approval result channel full or closed for %s (timeout may have fired)", s.SessionID, approvalID)
	}
}

// SetApprovalTimeout sets the timeout for approval requests
func (s *WSSession) SetApprovalTimeout(timeout time.Duration) {
	s.approvalTimeout = timeout
}

// SetReadTimeout sets the read timeout used to reset the read deadline after writes.
func (s *WSSession) SetReadTimeout(d time.Duration) {
	s.readTimeout = d
}

// WSChatHandler implements Handler for WebSocket output
type WSChatHandler struct {
	session *WSSession
}

// SendMessageCount sends the current message count to the client
func (s *WSSession) SendMessageCount() {
	count := 0
	if s.ChatSession != nil {
		count = s.ChatSession.GetMessageCount()
	}
	s.SendMessage("message_count", map[string]int{
		"count": count,
	})
}

func NewWSChatHandler(session *WSSession) *WSChatHandler {
	return &WSChatHandler{session: session}
}

func (h *WSChatHandler) SendChunk(content string, first, last bool, contentType string) {
	h.session.SendChunk(content, first, last, contentType)
}

func (h *WSChatHandler) SendToolCall(name string, arguments string, id string, streaming bool) {
	h.session.SendMessage("tool_call", map[string]interface{}{
		"name":      name,
		"arguments": arguments,
		"index":     id,
		"streaming": streaming,
	})
}

func (h *WSChatHandler) SendThinking(status bool) {
	h.session.SendMessage("thinking", map[string]interface{}{"status": status})
}

func (h *WSChatHandler) SendComplete(message string) {
	h.session.SendMessage("complete", map[string]interface{}{"message": message})
}

func (h *WSChatHandler) SendError(err string) {
	log.Printf("SendError: %v\n", err)
	h.session.SendError(err)
}

// SendApprovalRequest sends an approval request to the client and waits for the result
func (h *WSChatHandler) SendMessageCount() {
	if h.session != nil {
		h.session.SendMessageCount()
	}
}

func (h *WSChatHandler) SendApprovalRequest(targets []ApprovalTarget) (ApprovalResultMap, error) {
	session := h.session

	// Generate a unique approval ID
	approvalID := generateApprovalID()
	log.Printf("Session %s: Sending approval request %s for %d targets", session.SessionID, approvalID, len(targets))

	// Create a channel to receive the result
	resultChan := make(chan ApprovalResultMap, 1)
	req := &ApprovalRequest{
		ApprovalID: approvalID,
		ResultChan: resultChan,
	}

	// Convert targets to a format suitable for JSON
	targetList := make([]map[string]interface{}, len(targets))
	for i, t := range targets {
		targetList[i] = map[string]interface{}{
			"id":      t.ID,
			"tool":    t.ToolName,
			"details": t.ArgumentsInfo,
		}
	}

	// Store pending approval request (thread-safe)
	session.approvalMu.Lock()
	if session.pendingApproval != nil {
		session.approvalMu.Unlock()
		log.Printf("Session %s: Approval channel busy with pending request %s", session.SessionID, session.pendingApproval.ApprovalID)
		return nil, fmt.Errorf("approval channel is busy")
	}
	session.pendingApproval = req
	session.approvalMu.Unlock()

	// Send approval request to client
	log.Printf("Session %s: Sending approval_request message for %s", session.SessionID, approvalID)
	session.SendMessage("approval_request", map[string]interface{}{
		"approval_id": approvalID,
		"targets":     targetList,
	})

	// Wait for response with timeout
	timeout := session.approvalTimeout
	if timeout <= 0 {
		timeout = DefaultApprovalTimeout
	}
	log.Printf("Session %s: Waiting for approval response for %s (timeout: %v)", session.SessionID, approvalID, timeout)

	select {
	case result := <-resultChan:
		log.Printf("Session %s: Received approval response for %s with %d results", session.SessionID, approvalID, len(result))
		// Clear pending approval
		session.approvalMu.Lock()
		session.pendingApproval = nil
		session.approvalMu.Unlock()

		if result == nil {
			return nil, fmt.Errorf("approval request got stale response")
		}
		return result, nil
	case <-time.After(timeout):
		log.Printf("Session %s: Approval request %s timed out after %v", session.SessionID, approvalID, timeout)

		// Clear pending approval on timeout
		session.approvalMu.Lock()
		if session.pendingApproval != nil && session.pendingApproval.ApprovalID == approvalID {
			session.pendingApproval = nil
		}
		session.approvalMu.Unlock()

		return nil, fmt.Errorf("approval request timed out after %v", timeout)
	}
}

// generateApprovalID generates a unique approval request ID
func generateApprovalID() string {
	return fmt.Sprintf("approval-%d", time.Now().UnixNano())
}
