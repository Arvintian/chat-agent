package serve

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// EventHandler is the interface that clients must implement to receive server events.
// All methods are called from the read loop goroutine; implementations should
// be thread-safe or delegate work to other goroutines as needed.
type EventHandler interface {
	// OnSessionInit is called when the server sends the session_init message.
	OnSessionInit(payload *SessionInitPayload)

	// OnChatSelected is called when a chat is successfully selected.
	OnChatSelected(payload *ChatSelectedPayload)

	// OnChunk is called for each streaming content chunk received.
	OnChunk(payload *ChunkPayload)

	// OnToolCall is called when the model invokes a tool.
	OnToolCall(payload *ToolCallPayload)

	// OnThinking is called when the thinking/reasoning state changes.
	OnThinking(payload *ThinkingPayload)

	// OnComplete is called when the model response is complete.
	OnComplete(payload *CompletePayload)

	// OnError is called when an error occurs.
	OnError(payload *ErrorPayload)

	// OnApprovalRequest is called when tool execution requires user approval.
	// The handler should call SendApprovalResponse to provide the decision.
	OnApprovalRequest(payload *ApprovalRequestPayload)

	// OnMessageCount is called when the message count changes.
	OnMessageCount(payload *MessageCountPayload)

	// OnStopped is called when a response is stopped by the user.
	OnStopped(payload *StoppedPayload)

	// OnKept is called after a keep hook executes.
	OnKept(payload *KeptPayload)

	// OnCleared is called after the conversation context is cleared.
	OnCleared(payload *ClearedPayload)

	// OnDisconnected is called when the WebSocket connection is lost.
	// err is nil for intentional disconnection.
	OnDisconnected(err error)

	// OnReconnected is called when the client successfully reconnects.
	OnReconnected()
}

// Client is a WebSocket client SDK for the chat-agent serve mode.
// It manages the connection lifecycle and message passing.
type Client struct {
	serverURL string

	// Configuration
	headers        http.Header
	basicAuthUser  string
	basicAuthPass  string
	reconnectDelay time.Duration
	maxReconnect   int
	writeTimeout   time.Duration
	readTimeout    time.Duration
	sessionID      string

	// Internal state
	conn       *websocket.Conn
	connMu     sync.Mutex
	handler    EventHandler
	closed     atomic.Bool
	reconnects atomic.Int32

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// Write channel to serialize outgoing messages
	writeCh chan *WSMessage
}

// NewClient creates a new WebSocket client for the given server URL.
//
// The serverURL should be a full WebSocket URL, e.g. "ws://localhost:8080/ws".
// Options can be used to configure authentication, reconnection, timeouts, etc.
//
// Example:
//
//	client := serve.NewClient("ws://localhost:8080/ws",
//	    serve.WithBasicAuth("user", "pass"),
//	    serve.WithReconnect(2*time.Second, 5),
//	)
//	client.SetEventHandler(myHandler)
//	if err := client.Connect(); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
func NewClient(serverURL string, opts ...ClientOption) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Client{
		serverURL:      serverURL,
		headers:        make(http.Header),
		reconnectDelay: DefaultReconnectDelay,
		maxReconnect:   DefaultMaxReconnect,
		writeTimeout:   DefaultWriteTimeout,
		readTimeout:    DefaultReadTimeout,
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		writeCh:        make(chan *WSMessage, 64),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// SetEventHandler registers the handler to receive server events.
// Must be called before Connect.
func (c *Client) SetEventHandler(handler EventHandler) {
	c.handler = handler
}

// SessionID returns the current session ID (available after session_init).
func (c *Client) SessionID() string {
	return c.sessionID
}

// Connect establishes a WebSocket connection to the server.
// It blocks until the connection is established or an error occurs.
func (c *Client) Connect() error {
	if c.handler == nil {
		return fmt.Errorf("event handler not set; call SetEventHandler before Connect")
	}

	if err := c.dial(); err != nil {
		return err
	}

	// Start background loops
	go c.readLoop()
	go c.writeLoop()

	return nil
}

// Close gracefully closes the WebSocket connection and stops all goroutines.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.cancel()

	// Send a close frame
	c.connMu.Lock()
	if c.conn != nil {
		msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "client closing")
		c.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(c.writeTimeout))
		c.conn.Close()
	}
	c.connMu.Unlock()

	<-c.done
	return nil
}

// IsConnected returns whether the client is currently connected.
func (c *Client) IsConnected() bool {
	return !c.closed.Load()
}

// ---- Command methods (thread-safe) ----

// SelectChat selects a chat by name. Must be called before sending messages.
func (c *Client) SelectChat(chatName string) error {
	return c.sendCommand(CmdSelectChat, ChatRequest{ChatName: chatName})
}

// SendMessage sends a text message to the currently selected chat.
func (c *Client) SendMessage(text string) error {
	return c.sendCommand(CmdChat, ChatRequest{Message: text})
}

// SendMessageWithFiles sends a text message with file attachments.
func (c *Client) SendMessageWithFiles(text string, files []FilePayload) error {
	return c.sendCommand(CmdChat, ChatRequest{Message: text, Files: files})
}

// Regenerate requests regeneration of the last response.
func (c *Client) Regenerate() error {
	return c.sendCommand(CmdRegenerate, ChatRequest{})
}

// Stop stops the current ongoing response.
func (c *Client) Stop() error {
	return c.sendCommand(CmdStop, nil)
}

// Clear clears the conversation context for the current chat.
func (c *Client) Clear() error {
	return c.sendCommand(CmdClear, nil)
}

// Keep triggers the keep hook for the current session.
func (c *Client) Keep() error {
	return c.sendCommand(CmdKeep, nil)
}

// DeselectChat deselects the current chat and returns to the selection page.
func (c *Client) DeselectChat() error {
	return c.sendCommand(CmdDeselectChat, nil)
}

// SendApprovalResponse sends the user's approval decision back to the server.
func (c *Client) SendApprovalResponse(approvalID string, results map[string]ApprovalItem) error {
	return c.sendCommand(CmdApprovalResponse, ApprovalResponsePayload{
		ApprovalID: approvalID,
		Results:    results,
	})
}

// ---- Internal methods ----

func (c *Client) sendCommand(cmdType string, payload interface{}) error {
	if c.closed.Load() {
		return fmt.Errorf("client is closed")
	}

	var rawPayload json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %w", err)
		}
		rawPayload = data
	}

	msg := &WSMessage{
		Type:    cmdType,
		Payload: rawPayload,
	}

	select {
	case c.writeCh <- msg:
		return nil
	case <-c.ctx.Done():
		return fmt.Errorf("client is closed")
	}
}

func (c *Client) dial() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	// Build headers
	headers := c.headers.Clone()
	if c.basicAuthUser != "" || c.basicAuthPass != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(c.basicAuthUser + ":" + c.basicAuthPass))
		headers.Set("Authorization", "Basic "+auth)
	}

	// Build URL with optional session_id
	u, err := url.Parse(c.serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	if c.sessionID != "" {
		q := u.Query()
		q.Set("session_id", c.sessionID)
		u.RawQuery = q.Encode()
	}

	conn, _, err := dialer.Dial(u.String(), headers)
	if err != nil {
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	// Set ping handler to respond to server pings with a pong and refresh the read deadline.
	// This keeps the connection alive and prevents read timeouts during idle periods.
	conn.SetPingHandler(func(appData string) error {
		if c.readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(c.readTimeout))
		}
		if c.writeTimeout > 0 {
			conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
		}
		return conn.WriteMessage(websocket.PongMessage, []byte(appData))
	})

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	return nil
}

// readLoop reads messages from the WebSocket connection and dispatches to the handler.
func (c *Client) readLoop() {
	defer close(c.done)

	for {
		if c.closed.Load() {
			return
		}

		c.connMu.Lock()
		conn := c.conn
		c.connMu.Unlock()

		if conn == nil {
			return
		}

		if c.readTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(c.readTimeout))
		}

		_, raw, err := conn.ReadMessage()
		if err != nil {
			if c.closed.Load() {
				return
			}

			if c.handler != nil {
				c.handler.OnDisconnected(err)
			}

			// Attempt reconnection
			if c.reconnectDelay > 0 {
				if c.maxReconnect <= 0 || int(c.reconnects.Load()) < c.maxReconnect {
					c.tryReconnect()
					continue
				}
			}

			return
		}

		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("serve sdk: failed to unmarshal message: %v", err)
			continue
		}

		c.dispatch(&msg)
	}
}

// writeLoop serializes writes to the WebSocket connection.
func (c *Client) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg := <-c.writeCh:
			c.connMu.Lock()
			conn := c.conn
			c.connMu.Unlock()

			if conn == nil {
				continue
			}

			if c.writeTimeout > 0 {
				conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
			}

			if err := conn.WriteJSON(msg); err != nil {
				log.Printf("serve sdk: write error: %v", err)
			}
		}
	}
}

// tryReconnect attempts to re-establish the connection.
func (c *Client) tryReconnect() {
	c.reconnects.Add(1)
	defer c.reconnects.Add(-1)

	log.Printf("serve sdk: attempting reconnect (attempt %d)", c.reconnects.Load())

	time.Sleep(c.reconnectDelay)

	if c.closed.Load() {
		return
	}

	if err := c.dial(); err != nil {
		log.Printf("serve sdk: reconnect failed: %v", err)
		return
	}

	if c.handler != nil {
		c.handler.OnReconnected()
	}

	log.Printf("serve sdk: reconnected successfully")
}

// dispatch routes a parsed server message to the appropriate handler method.
func (c *Client) dispatch(msg *WSMessage) {
	if c.handler == nil {
		return
	}

	switch msg.Type {
	case MsgSessionInit:
		var payload SessionInitPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.sessionID = payload.SessionID
			c.handler.OnSessionInit(&payload)
		}
	case MsgChatSelected:
		var payload ChatSelectedPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnChatSelected(&payload)
		}
	case MsgChunk:
		var payload ChunkPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnChunk(&payload)
		}
	case MsgToolCall:
		var payload ToolCallPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnToolCall(&payload)
		}
	case MsgThinking:
		var payload ThinkingPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnThinking(&payload)
		}
	case MsgComplete:
		var payload CompletePayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnComplete(&payload)
		}
	case MsgError:
		var payload ErrorPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnError(&payload)
		}
	case MsgApprovalRequest:
		var payload ApprovalRequestPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnApprovalRequest(&payload)
		}
	case MsgMessageCount:
		var payload MessageCountPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnMessageCount(&payload)
		}
	case MsgStopped:
		var payload StoppedPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnStopped(&payload)
		}
	case MsgKept:
		var payload KeptPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnKept(&payload)
		}
	case MsgCleared:
		var payload ClearedPayload
		if c.unmarshalPayload(msg.Payload, &payload) {
			c.handler.OnCleared(&payload)
		}
	default:
		log.Printf("serve sdk: unknown message type: %s", msg.Type)
	}
}

func (c *Client) unmarshalPayload(raw json.RawMessage, v interface{}) bool {
	if err := json.Unmarshal(raw, v); err != nil {
		log.Printf("serve sdk: failed to unmarshal %T payload: %v", v, err)
		return false
	}
	return true
}
