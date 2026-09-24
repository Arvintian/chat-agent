package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
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

// ConnectionChat holds the per-chat state attached to a single WebSocket
// connection. A connection can attach several chats at the same time and
// stream responses from multiple chats concurrently; every event sent to the
// client carries the chat name so the client can route it to the right view.
type ConnectionChat struct {
	Name        string
	ChatSession *ChatSession
	ChatBot     *ChatBot
	WSHandler   *WSChatHandler

	session *WSSession

	// inFlight reports whether a response stream is currently running for
	// this chat (at most one stream per chat).
	inFlight atomic.Bool

	// Cancel state for stopping the ongoing stream of this chat.
	cancelMu    sync.Mutex
	cancelFunc  context.CancelFunc
	isCancelled bool
}

// SetCancelled marks the chat's stream as cancelled
func (c *ConnectionChat) SetCancelled() {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	if !c.isCancelled {
		c.isCancelled = true
		if c.cancelFunc != nil {
			c.cancelFunc()
		}
	}
}

// IsCancelled returns whether the chat's stream is cancelled
func (c *ConnectionChat) IsCancelled() bool {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	return c.isCancelled
}

// ResetCancel resets the cancel state for a new request
func (c *ConnectionChat) ResetCancel() {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	if c.isCancelled {
		c.isCancelled = false
		c.cancelFunc = nil
	}
}

// SetCancelFunc sets the cancel function for the current request
func (c *ConnectionChat) SetCancelFunc(cancelFunc context.CancelFunc) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	c.cancelFunc = cancelFunc
}

// InFlight exposes the in-flight flag (atomic.Bool) for busy checks.
func (c *ConnectionChat) InFlight() *atomic.Bool {
	return &c.inFlight
}

// WSSession represents a WebSocket session with its connection.
// It holds only connection-level state; per-chat state lives in
// ConnectionChat entries returned by AttachChat/GetChat.
type WSSession struct {
	conn      *websocket.Conn
	connMu    sync.Mutex
	cfg       *config.Config
	SessionID string
	// CurrentChat is the last chat selected on this connection. It is used
	// as the fallback target for commands that carry no chat_name (legacy
	// single-chat clients).
	CurrentChat string

	chatsMu sync.RWMutex
	chats   map[string]*ConnectionChat

	// closed is set to true when the connection is closing, to prevent
	// writes to a closed connection from in-flight goroutines.
	closed atomic.Bool

	// readTimeout is used to reset the read deadline after a successful write.
	// This prevents SendMessage from starving SendPing to the point where
	// ReadMessage's pongWait expires.
	readTimeout time.Duration

	// Approval state for handling authorization requests. Multiple chats can
	// hold pending approval requests concurrently, keyed by approval ID.
	approvalTimeout  time.Duration
	pendingApprovals map[string]*ApprovalRequest
	approvalMu       sync.Mutex
}

func NewWSSession(conn *websocket.Conn, sessionID string, cfg *config.Config) *WSSession {
	return &WSSession{
		conn:             conn,
		cfg:              cfg,
		SessionID:        sessionID,
		CurrentChat:      "",
		chats:            make(map[string]*ConnectionChat),
		approvalTimeout:  DefaultApprovalTimeout,
		pendingApprovals: make(map[string]*ApprovalRequest),
	}
}

// AttachChat registers (or re-attaches) a chat on this connection and (re)
// binds its ChatBot output handler to this connection. It returns the
// ConnectionChat entry which callers may further update.
func (s *WSSession) AttachChat(name string, chatSession *ChatSession, chatBot *ChatBot) *ConnectionChat {
	s.chatsMu.Lock()
	defer s.chatsMu.Unlock()
	cc, ok := s.chats[name]
	if !ok {
		cc = &ConnectionChat{Name: name, session: s}
		s.chats[name] = cc
	}
	cc.ChatSession = chatSession
	cc.ChatBot = chatBot
	cc.WSHandler = NewWSChatHandler(cc)
	if chatBot != nil {
		chatBot.SetHandler(cc.WSHandler)
	}
	return cc
}

// GetChat returns the chat attached to this connection, or nil.
func (s *WSSession) GetChat(name string) *ConnectionChat {
	s.chatsMu.RLock()
	defer s.chatsMu.RUnlock()
	return s.chats[name]
}

// DetachChat removes a chat from this connection (state is kept in the
// session manager for later restoration).
func (s *WSSession) DetachChat(name string) {
	s.chatsMu.Lock()
	defer s.chatsMu.Unlock()
	delete(s.chats, name)
}

// ChatNames returns the names of all chats attached to this connection.
func (s *WSSession) ChatNames() []string {
	s.chatsMu.RLock()
	defer s.chatsMu.RUnlock()
	names := make([]string, 0, len(s.chats))
	for name := range s.chats {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CancelAllInFlight cancels the stream of every attached chat that is
// currently generating. Used when the connection closes.
func (s *WSSession) CancelAllInFlight() {
	s.chatsMu.RLock()
	defer s.chatsMu.RUnlock()
	for _, cc := range s.chats {
		cc.SetCancelled()
	}
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
	// causing a premature pong timeout.
	if s.readTimeout > 0 {
		s.conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	}
}

// SendPing sends a WebSocket ping frame to the client.
// Used for keepalive to detect dead connections.
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

// SendError sends a connection-level (not chat-scoped) error message.
func (s *WSSession) SendError(errMsg string) {
	s.SendMessage("error", map[string]string{"error": errMsg})
}

// HandleApprovalResponse processes an approval response from the client.
// Approval requests from different chats can be pending concurrently and are
// matched by approval ID.
func (s *WSSession) HandleApprovalResponse(approvalID string, results ApprovalResultMap) {
	s.approvalMu.Lock()
	req, ok := s.pendingApprovals[approvalID]
	if !ok {
		s.approvalMu.Unlock()
		log.Printf("Session %s: No pending approval request for %s", s.SessionID, approvalID)
		return
	}
	delete(s.pendingApprovals, approvalID)
	s.approvalMu.Unlock()

	log.Printf("Session %s: Received approval response for %s with %d results", s.SessionID, approvalID, len(results))

	// Send result to waiting request using non-blocking send
	// This ensures we don't block the WebSocket read loop
	select {
	case req.ResultChan <- results:
		log.Printf("Session %s: Approval result sent successfully for %s", s.SessionID, approvalID)
	default:
		// Channel might be full (timeout already fired) or closed
		log.Printf("Session %s: Approval result channel full or closed for %s (timeout may have fired)", s.SessionID, approvalID)
	}
}

// SetApprovalTimeout sets the timeout for approval requests
func (s *WSSession) SetApprovalTimeout(timeout time.Duration) {
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	s.approvalTimeout = timeout
}

// SetReadTimeout sets the read timeout used to reset the read deadline after writes.
func (s *WSSession) SetReadTimeout(d time.Duration) {
	s.readTimeout = d
}

// WSChatHandler implements Handler for WebSocket output of a single chat.
// Every event it sends carries the chat name in the payload.
type WSChatHandler struct {
	cc *ConnectionChat
}

func NewWSChatHandler(cc *ConnectionChat) *WSChatHandler {
	return &WSChatHandler{cc: cc}
}

func (h *WSChatHandler) SendChunk(content string, first, last bool, contentType string) {
	h.cc.session.SendMessage("chunk", map[string]interface{}{
		"chat_name":    h.cc.Name,
		"content":      content,
		"first":        first,
		"last":         last,
		"content_type": contentType,
	})
}

func (h *WSChatHandler) SendToolCall(name string, arguments string, id string, streaming bool) {
	h.cc.session.SendMessage("tool_call", map[string]interface{}{
		"chat_name": h.cc.Name,
		"name":      name,
		"arguments": arguments,
		"index":     id,
		"streaming": streaming,
	})
}

func (h *WSChatHandler) SendThinking(status bool) {
	h.cc.session.SendMessage("thinking", map[string]interface{}{
		"chat_name": h.cc.Name,
		"status":    status,
	})
}

func (h *WSChatHandler) SendComplete(message string) {
	h.cc.session.SendMessage("complete", map[string]interface{}{
		"chat_name": h.cc.Name,
		"message":   message,
	})
}

func (h *WSChatHandler) SendError(err string) {
	log.Printf("SendError: %v\n", err)
	h.cc.session.SendMessage("error", map[string]interface{}{
		"chat_name": h.cc.Name,
		"error":     err,
	})
}

// SendMessageCount sends the current message count of this chat to the client
func (h *WSChatHandler) SendMessageCount() {
	count := 0
	if h.cc.ChatSession != nil {
		count = h.cc.ChatSession.GetMessageCount()
	}
	h.cc.session.SendMessage("message_count", map[string]interface{}{
		"chat_name": h.cc.Name,
		"count":     count,
	})
}

// SendApprovalRequest sends an approval request to the client and waits for
// the result. Requests from different chats do not block each other.
func (h *WSChatHandler) SendApprovalRequest(targets []ApprovalTarget) (ApprovalResultMap, error) {
	session := h.cc.session

	// Generate a unique approval ID
	approvalID := generateApprovalID()
	log.Printf("Session %s: Sending approval request %s for chat %s with %d targets",
		session.SessionID, approvalID, h.cc.Name, len(targets))

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

	// Store pending approval request (thread-safe, keyed by approval ID)
	session.approvalMu.Lock()
	session.pendingApprovals[approvalID] = req
	session.approvalMu.Unlock()

	// Send approval request to client
	log.Printf("Session %s: Sending approval_request message for %s", session.SessionID, approvalID)
	session.SendMessage("approval_request", map[string]interface{}{
		"approval_id": approvalID,
		"chat_name":   h.cc.Name,
		"targets":     targetList,
	})

	// Wait for response with timeout
	session.approvalMu.Lock()
	timeout := session.approvalTimeout
	session.approvalMu.Unlock()
	if timeout <= 0 {
		timeout = DefaultApprovalTimeout
	}
	log.Printf("Session %s: Waiting for approval response for %s (timeout: %v)", session.SessionID, approvalID, timeout)

	select {
	case result := <-resultChan:
		log.Printf("Session %s: Received approval response for %s with %d results", session.SessionID, approvalID, len(result))
		session.approvalMu.Lock()
		delete(session.pendingApprovals, approvalID)
		session.approvalMu.Unlock()

		if result == nil {
			return nil, fmt.Errorf("approval request got stale response")
		}
		return result, nil
	case <-time.After(timeout):
		log.Printf("Session %s: Approval request %s timed out after %v", session.SessionID, approvalID, timeout)

		// Clear pending approval on timeout
		session.approvalMu.Lock()
		if _, ok := session.pendingApprovals[approvalID]; ok {
			delete(session.pendingApprovals, approvalID)
		}
		session.approvalMu.Unlock()

		return nil, fmt.Errorf("approval request timed out after %v", timeout)
	}
}

// generateApprovalID generates a unique approval request ID
func generateApprovalID() string {
	return fmt.Sprintf("approval-%d", time.Now().UnixNano())
}
