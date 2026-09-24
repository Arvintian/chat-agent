package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/web"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/spf13/cobra"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey string

const authUserKey contextKey = "auth_user"

// BasicAuthMiddleware creates a middleware for HTTP Basic Authentication.
// It accepts a map of username->password pairs and authenticates against any of them.
// On successful auth, the username is stored in the request context under authUserKey.
func BasicAuthMiddleware(credentials map[string]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if no credentials are configured
			if len(credentials) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			writeUnauthorized := func() {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeUnauthorized()
				return
			}

			// Extract credentials from Authorization header
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
				writeUnauthorized()
				return
			}

			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				writeUnauthorized()
				return
			}

			credentialParts := strings.SplitN(string(decoded), ":", 2)
			if len(credentialParts) != 2 {
				writeUnauthorized()
				return
			}

			receivedUser := credentialParts[0]
			receivedPass := credentialParts[1]

			expectedPass, ok := credentials[receivedUser]
			if !ok || receivedPass != expectedPass {
				writeUnauthorized()
				return
			}

			// Store authenticated user in context for access logging
			ctx := context.WithValue(r.Context(), authUserKey, receivedUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code and response size.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += int64(n)
	return n, err
}

// Hijack implements http.Hijacker so that WebSocket upgrades work through the wrapper.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("websocket: response does not implement http.Hijacker")
}

// AccessLogMiddleware logs each HTTP request in a combined-log-like format.
// If basic auth is active, the authenticated username is included.
func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: 0}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		// Extract authenticated user from context (set by BasicAuthMiddleware)
		user := "-"
		if u, ok := r.Context().Value(authUserKey).(string); ok && u != "" {
			user = u
		}

		log.Printf("%s - %s \"%s %s %s\" %d %d %s",
			r.RemoteAddr,
			user,
			r.Method,
			r.RequestURI,
			r.Proto,
			rw.statusCode,
			rw.size,
			duration,
		)
	})
}

// parseBasicAuth parses a comma-separated list of "user:pass" pairs into a map.
// Empty or malformed input returns an empty map (auth disabled).
func parseBasicAuth(raw string) map[string]string {
	credentials := make(map[string]string)
	if raw == "" {
		return credentials
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) != 2 {
			continue
		}
		user := strings.TrimSpace(parts[0])
		pass := strings.TrimSpace(parts[1])
		if user != "" {
			credentials[user] = pass
		}
	}
	return credentials
}

// parseBasicAuthFile reads a file containing "user:password" pairs (one per line)
// and returns them as a credentials map. Empty lines and lines starting with "#" are skipped.
func parseBasicAuthFile(path string) (map[string]string, error) {
	credentials := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read basic auth file %s: %w", path, err)
	}
	for lineNum, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			log.Printf("Warning: skipping malformed line %d in %s: %s", lineNum+1, path, line)
			continue
		}
		user := strings.TrimSpace(parts[0])
		pass := strings.TrimSpace(parts[1])
		if user != "" {
			credentials[user] = pass
		}
	}
	return credentials, nil
}

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start chat-agent in web mode with WebSocket support",
	Long: `Start chat-agent as a WebSocket server for web-based chat interactions.

Each client connection can select a chat and start independent conversation sessions.

Examples:
  chat-agent serve --port 8080
  chat-agent serve --port 8080 --basic-auth "alice:pwd1,bob:pwd2"
  chat-agent serve --port 8080 --basic-auth-file /etc/chat-agent/users`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := logger.Init(); err != nil {
			return err
		}
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return err
		}

		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")
		welcome, _ := cmd.Flags().GetString("welcome")
		basicAuth, _ := cmd.Flags().GetString("basic-auth")
		basicAuthFile, _ := cmd.Flags().GetString("basic-auth-file")

		// Merge credentials: start with file-based, then overlay inline (inline takes precedence)
		credentials := make(map[string]string)
		if basicAuthFile != "" {
			fileCreds, err := parseBasicAuthFile(basicAuthFile)
			if err != nil {
				return err
			}
			for u, p := range fileCreds {
				credentials[u] = p
			}
		}
		for u, p := range parseBasicAuth(basicAuth) {
			credentials[u] = p
		}

		wsHandler := NewWebSocketHandler(cfg)

		authMiddleware := BasicAuthMiddleware(credentials)

		router := mux.NewRouter()
		router.Use(authMiddleware)
		router.Use(AccessLogMiddleware)
		router.HandleFunc("/ws", wsHandler.HandleWebSocket)

		router.HandleFunc("/chats", func(w http.ResponseWriter, r *http.Request) {
			type ChatInfo struct {
				Name        string `json:"name"`
				HasKeepHook bool   `json:"has_keep_hook"`
			}
			chats := make([]ChatInfo, 0, len(cfg.Chats))
			defaultChat := ""
			for name, chatCfg := range cfg.Chats {
				hasKeepHook := chatCfg.Hooks != nil && chatCfg.Hooks.Keep != nil && chatCfg.Hooks.Keep.Enabled
				chats = append(chats, ChatInfo{
					Name:        name,
					HasKeepHook: hasKeepHook,
				})
				if chatCfg.Default {
					defaultChat = name
				}
			}
			// Sort by chat name
			sort.Slice(chats, func(i, j int) bool {
				return chats[i].Name < chats[j].Name
			})

			// Get active chats for this session (if session_id provided)
			activeChats := make(map[string]bool)
			sessionID := r.URL.Query().Get("session_id")
			if sessionID != "" {
				activeChats = wsHandler.sessionManager.getActiveChats(sessionID)
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"chats":        chats,
				"default_chat": defaultChat,
				"active_chats": activeChats,
			})
		})

		router.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			title := welcome
			if title == "" {
				title = "Chat-Agent"
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"webui": map[string]interface{}{
					"title": title,
				},
			})
		})

		router.PathPrefix("/").Handler(web.StaticHandler())

		addr := fmt.Sprintf("%s:%d", host, port)
		log.Printf("Starting chat-agent web server on %s", addr)
		log.Printf("WebSocket endpoint: ws://%s/ws", addr)
		log.Printf("HTTP endpoint: http://%s/", addr)

		server := &http.Server{
			Addr:    addr,
			Handler: router,
		}

		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("Server error: %v", err)
			}
		}()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Printf("Shutting down server...")

		// Cleanup all sessions on server shutdown
		wsHandler.sessionManager.CloseAllSessions()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}

		log.Printf("Server stopped")
		return nil
	},
}

// FilePayload represents a file in the chat request
type FilePayload struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	FileSize int64  `json:"file_size,omitempty"`
}

type ChatRequest struct {
	ChatName string        `json:"chat_name"`
	Message  string        `json:"message"`
	Files    []FilePayload `json:"files,omitempty"`
}

// ChatState represents the state of a single chat within a session
type ChatState struct {
	ChatSession *chatbot.ChatSession
	ChatBot     *chatbot.ChatBot
}

type SessionInfo struct {
	ID        string
	ChatName  string                // Current active chat
	Chats     map[string]*ChatState // All chats in this session
	CreatedAt time.Time
}

// ApprovalResponsePayload represents the approval response from the client
type ApprovalResponsePayload struct {
	ApprovalID string                  `json:"approval_id"`
	Results    map[string]ApprovalItem `json:"results"`
}

// ApprovalItem represents a single approval result
type ApprovalItem struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

// WebSocket ping/pong configuration
const (
	// Time allowed to read the next pong message from the peer
	pongWait = 5 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 8) / 10
)

// WebSocket upgrader
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins in development
	},
}

// maxConcurrentStreamsPerSession limits how many chat responses may stream
// in parallel within one session (across all of its connections).
const maxConcurrentStreamsPerSession = 3

// SessionManager manages chat sessions
type SessionManager struct {
	sessions map[string]*SessionInfo
	cfg      *config.Config
	mu       sync.RWMutex
	// connectionCount tracks the number of active WebSocket connections per session
	connectionCount map[string]int
	// activeChats tracks which chats are currently active per session
	// sessionId -> chatName -> connection count
	activeChats map[string]map[string]int
	// streamCount tracks the number of in-flight response streams per session
	streamCount map[string]int
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		sessions:        make(map[string]*SessionInfo),
		cfg:             cfg,
		connectionCount: make(map[string]int),
		activeChats:     make(map[string]map[string]int),
		streamCount:     make(map[string]int),
	}
}

// tryStartStream reserves a concurrent stream slot for a session.
// Returns false when the session already has maxConcurrentStreamsPerSession
// in-flight streams.
func (sm *SessionManager) tryStartStream(sessionID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.streamCount[sessionID] >= maxConcurrentStreamsPerSession {
		return false
	}
	sm.streamCount[sessionID]++
	return true
}

// stopStream releases a concurrent stream slot for a session.
func (sm *SessionManager) stopStream(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.streamCount[sessionID] > 0 {
		sm.streamCount[sessionID]--
		if sm.streamCount[sessionID] == 0 {
			delete(sm.streamCount, sessionID)
		}
	}
}

// tryRegisterConnection increments the connection count for a session.
// Multiple tabs/windows can share the same session.
func (sm *SessionManager) tryRegisterConnection(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.connectionCount[sessionID]++
	log.Printf("Session %s: connection count increased to %d", sessionID, sm.connectionCount[sessionID])
}

// markChatActive increments the active count for a chat in a session.
// Returns true if this is the first connection to activate this chat.
func (sm *SessionManager) markChatActive(sessionID, chatName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.activeChats[sessionID] == nil {
		sm.activeChats[sessionID] = make(map[string]int)
	}
	sm.activeChats[sessionID][chatName]++
	log.Printf("Session %s: chat '%s' active count increased to %d", sessionID, chatName, sm.activeChats[sessionID][chatName])
}

// markChatInactive decrements the active count for a chat in a session.
func (sm *SessionManager) markChatInactive(sessionID, chatName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if chats, ok := sm.activeChats[sessionID]; ok {
		if count, ok := chats[chatName]; ok {
			if count <= 1 {
				delete(chats, chatName)
				if len(chats) == 0 {
					delete(sm.activeChats, sessionID)
				}
			} else {
				chats[chatName] = count - 1
			}
		}
	}
	log.Printf("Session %s: chat '%s' active count decreased", sessionID, chatName)
}

// isChatActive checks if a chat is already active in another connection of the same session.
func (sm *SessionManager) isChatActive(sessionID, chatName string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if chats, ok := sm.activeChats[sessionID]; ok {
		return chats[chatName] > 0
	}
	return false
}

// getActiveChats returns the set of active chat names for a session.
func (sm *SessionManager) getActiveChats(sessionID string) map[string]bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make(map[string]bool)
	if chats, ok := sm.activeChats[sessionID]; ok {
		for name := range chats {
			result[name] = true
		}
	}
	return result
}

// unregisterConnection decrements the connection count for a session.
func (sm *SessionManager) unregisterConnection(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if count, ok := sm.connectionCount[sessionID]; ok {
		if count <= 1 {
			delete(sm.connectionCount, sessionID)
		} else {
			sm.connectionCount[sessionID] = count - 1
		}
	}
	log.Printf("Session %s: connection count decreased to %d", sessionID, sm.connectionCount[sessionID])
}

func (sm *SessionManager) GetSession(sessionID string) (*SessionInfo, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[sessionID]
	return session, ok
}

func (sm *SessionManager) AddSession(sessionID string, chatName string, chatSession *chatbot.ChatSession) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.sessions[sessionID] == nil {
		chats := make(map[string]*ChatState)
		if chatName != "" && chatSession != nil {
			chats[chatName] = &ChatState{
				ChatSession: chatSession,
			}
		}
		sm.sessions[sessionID] = &SessionInfo{
			ID:        sessionID,
			ChatName:  chatName,
			Chats:     chats,
			CreatedAt: time.Now(),
		}
	}
}

// UpdateChatSessionWithBot updates session with both ChatSession and ChatBot for a specific chat
func (sm *SessionManager) UpdateChatSessionWithBot(sessionID string, chatName string, chatSession *chatbot.ChatSession, chatBot *chatbot.ChatBot) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[sessionID]; ok {
		session.ChatName = chatName
		if session.Chats == nil {
			session.Chats = make(map[string]*ChatState)
		}
		session.Chats[chatName] = &ChatState{
			ChatSession: chatSession,
			ChatBot:     chatBot,
		}
	} else {
		// Create new session info if not exists
		chats := make(map[string]*ChatState)
		chats[chatName] = &ChatState{
			ChatSession: chatSession,
			ChatBot:     chatBot,
		}
		sm.sessions[sessionID] = &SessionInfo{
			ID:        sessionID,
			ChatName:  chatName,
			Chats:     chats,
			CreatedAt: time.Now(),
		}
	}
}

// GetChatState gets the chat state for a specific chat in a session
func (sm *SessionManager) GetChatState(sessionID string, chatName string) (*ChatState, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if session.Chats == nil {
		return nil, false
	}
	state, ok := session.Chats[chatName]
	return state, ok
}

func (sm *SessionManager) RemoveSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, ok := sm.sessions[sessionID]
	if !ok {
		return
	}
	// Close all chat sessions in this session
	for chatName, state := range session.Chats {
		if state.ChatSession != nil {
			if err := state.ChatSession.Close(); err != nil {
				log.Printf("Error closing session %s chat %s: %v", sessionID, chatName, err)
			}
		}
	}
	delete(sm.sessions, sessionID)
}

func (sm *SessionManager) CloseAllSessions() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for sessionID := range sm.sessions {
		delete(sm.connectionCount, sessionID)
		delete(sm.activeChats, sessionID)
		delete(sm.streamCount, sessionID)
	}
	for sessionID, session := range sm.sessions {
		for chatName, state := range session.Chats {
			if state.ChatSession != nil {
				if err := state.ChatSession.Close(); err != nil {
					log.Printf("Error closing session %s chat %s: %v", sessionID, chatName, err)
				}
			}
		}
	}
	sm.sessions = make(map[string]*SessionInfo)
}

// WebSocketHandler handles WebSocket connections
type WebSocketHandler struct {
	sessionManager *SessionManager
	cfg            *config.Config
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(cfg *config.Config) *WebSocketHandler {
	return &WebSocketHandler{
		sessionManager: NewSessionManager(cfg),
		cfg:            cfg,
	}
}

func (h *WebSocketHandler) CloseAllSessions() {
	h.sessionManager.CloseAllSessions()
}

// HandleWebSocket handles a WebSocket connection
func (h *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Get or create session ID from query parameter
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	log.Printf("WebSocket connection: %s", sessionID)

	// Allow multiple tabs/windows to share the same session
	// Each tab gets its own WSSession wrapper but shares the underlying ChatSession
	h.sessionManager.tryRegisterConnection(sessionID)

	// Check if session already exists
	existingSession, exists := h.sessionManager.GetSession(sessionID)
	var session *chatbot.WSSession

	if exists && len(existingSession.Chats) > 0 {
		// Reuse existing session - create new WSSession with same ID but new connection
		// Don't auto-restore any chat - let the client explicitly select one.
		// This prevents conflicts when multiple tabs share a session.
		session = chatbot.NewWSSession(conn, sessionID, h.cfg)
		session.SetReadTimeout(pongWait)
		log.Printf("Reconnected to existing session %s with %d chats", sessionID, len(existingSession.Chats))
	} else {
		// Create new session
		session = chatbot.NewWSSession(conn, sessionID, h.cfg)
		session.SetReadTimeout(pongWait)
		h.sessionManager.AddSession(sessionID, "", nil)
		log.Printf("Created new session %s", sessionID)
	}

	// Send session ID to client
	session.SendMessage("session_init", map[string]interface{}{
		"session_id": sessionID,
	})

	// Configure ping/pong to detect dead connections (e.g., mobile network loss)
	// Set read deadline: if no pong is received within pongWait, the connection is considered dead.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Start a goroutine to send periodic pings
	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				session.SendPing()
			case <-pingDone:
				return
			}
		}
	}()

	// Ensure cleanup on connection close
	defer func() {
		// Mark session as closed first, so that any in-flight goroutines
		// (from processMessage) stop writing to the connection.
		session.MarkClosed()

		// Stop in-flight streams of every attached chat so they don't keep
		// consuming tokens after the client is gone.
		session.CancelAllInFlight()

		// Mark all chats active on this connection as inactive
		for _, chatName := range session.ChatNames() {
			h.sessionManager.markChatInactive(sessionID, chatName)
		}

		// Keep the session in memory if it still holds chat state, so a
		// reconnecting tab can restore its chats; otherwise drop it.
		if sess, ok := h.sessionManager.GetSession(sessionID); ok && len(sess.Chats) > 0 {
			log.Printf("Session %s disconnected (kept in memory, chats: %d)", sessionID, len(sess.Chats))
		} else {
			h.sessionManager.RemoveSession(sessionID)
			log.Printf("Session %s closed (no active chat)", sessionID)
		}
		// Unregister connection to allow reuse of session ID
		h.sessionManager.unregisterConnection(sessionID)
	}()

	// Handle messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for session %s: %v", sessionID, err)
			}
			break
		}

		var wsMsg chatbot.WSMessage
		if err := json.Unmarshal(message, &wsMsg); err != nil {
			log.Printf("Invalid message format: %v", err)
			session.SendError("Invalid message format")
			continue
		}

		go h.processMessage(session, &wsMsg)
	}
}

// processMessage processes a WebSocket message. Each message is handled in its
// own goroutine, so responses of several chats may stream concurrently.
func (h *WebSocketHandler) processMessage(session *chatbot.WSSession, msg *chatbot.WSMessage) {
	var req ChatRequest
	if msg.Payload != nil {
		// Ignore parse errors here: commands like stop/clear/keep may carry
		// empty payloads and handlers decide whether chat_name is required.
		_ = json.Unmarshal(msg.Payload, &req)
	}
	// Fall back to the last selected chat for legacy clients that don't
	// scope commands by chat_name.
	if req.ChatName == "" {
		req.ChatName = session.CurrentChat
	}

	switch msg.Type {
	case "select_chat":
		h.handleSelectChat(session, &req)
	case "chat":
		h.handleChat(session, &req)
	case "regenerate":
		// Remove last round (user message + assistant response) before re-processing
		if cc := session.GetChat(req.ChatName); cc != nil && cc.ChatSession != nil {
			cc.ChatSession.RemoveLastRound()
		}
		// Then process as normal chat
		h.handleChat(session, &req)
	case "stop":
		h.handleStop(session, &req)
	case "clear":
		h.handleClear(session, &req)
	case "keep":
		h.handleKeep(session, &req)
	case "approval_response":
		h.handleApprovalResponse(session, msg)
	case "deselect_chat":
		h.handleDeselectChat(session, &req)
	default:
		session.SendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
	}
}

// ensureChatActive marks a chat as active on its session unless it is
// already attached on this connection (no-op) or owned by another
// connection (error). Returns an empty string on success.
func (h *WebSocketHandler) ensureChatActive(session *chatbot.WSSession, chatName string) string {
	if session.GetChat(chatName) != nil {
		return "" // already attached on this connection (concurrent select_chat)
	}
	if h.sessionManager.isChatActive(session.SessionID, chatName) {
		return fmt.Sprintf("Chat '%s' is already active in another connection of this session", chatName)
	}
	h.sessionManager.markChatActive(session.SessionID, chatName)
	return ""
}

// sendChatError sends an error scoped to a chat (carries chat_name so the
// client can route it to the right view).
func (h *WebSocketHandler) sendChatError(session *chatbot.WSSession, chatName, errMsg string) {
	if chatName != "" {
		session.SendMessage("error", map[string]interface{}{
			"chat_name": chatName,
			"error":     errMsg,
		})
	} else {
		session.SendError(errMsg)
	}
}

// handleSelectChat opens (or re-activates) a chat on this connection.
// Unlike the legacy single-chat behavior, opening a new chat does NOT detach
// the previously opened ones: a connection may hold several chats at once and
// stream from them concurrently.
func (h *WebSocketHandler) handleSelectChat(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	if chatName == "" {
		h.sendChatError(session, "", "Invalid select_chat request: missing chat_name")
		return
	}

	// Verify chat exists
	chatCfg, ok := h.cfg.Chats[chatName]
	if !ok {
		h.sendChatError(session, chatName, fmt.Sprintf("Chat '%s' not found", chatName))
		return
	}

	var chatSession *chatbot.ChatSession
	var chatBot *chatbot.ChatBot
	message := fmt.Sprintf("Selected chat: %s", chatName)

	// Look up a saved state once (single read — a second lookup could observe
	// a concurrently removed entry and nil-deref below).
	var saved *ChatState
	if s, ok := h.sessionManager.GetChatState(session.SessionID, chatName); ok && s.ChatSession != nil {
		saved = s
	}

	// Already attached on this connection: just re-bind the handler and
	// mark as the current chat (client switched its view back to it).
	if cc := session.GetChat(chatName); cc != nil {
		chatSession, chatBot = cc.ChatSession, cc.ChatBot
		message = fmt.Sprintf("Reactivated chat: %s", chatName)
		log.Printf("Session %s: Reactivating existing chat session for '%s'", session.SessionID, chatName)
	} else if saved != nil {
		// Chat was opened before in this session: restore its saved state.
		if errMsg := h.ensureChatActive(session, chatName); errMsg != "" {
			h.sendChatError(session, chatName, errMsg)
			return
		}
		chatSession, chatBot = saved.ChatSession, saved.ChatBot
		message = fmt.Sprintf("Restored chat: %s", chatName)
		log.Printf("Session %s: Restoring existing chat session for '%s'", session.SessionID, chatName)
	} else {
		// Brand new chat: initialize a fresh chat session.
		if errMsg := h.ensureChatActive(session, chatName); errMsg != "" {
			h.sendChatError(session, chatName, errMsg)
			return
		}

		ctx := context.Background()
		var err error
		chatSession, err = chatbot.InitChatSession(ctx, h.cfg, chatName, session.SessionID, false)
		if err != nil {
			h.sessionManager.markChatInactive(session.SessionID, chatName)
			h.sendChatError(session, chatName, fmt.Sprintf("Failed to initialize chat session: %v", err))
			return
		}
		cb := chatbot.NewChatBot(ctx, chatSession.Agent, chatSession.Manager, nil, chatSession.PersistenceStore())
		chatBot = &cb
	}

	// Attach on this connection (re-binds the ChatBot output handler)
	session.AttachChat(chatName, chatSession, chatBot)
	session.CurrentChat = chatName

	// Keep the session manager in sync
	h.sessionManager.UpdateChatSessionWithBot(session.SessionID, chatName, chatSession, chatBot)

	// Get message count
	var msgCount int
	if chatSession != nil {
		msgCount = chatSession.GetMessageCount()
	}

	session.SendMessage("chat_selected", map[string]interface{}{
		"session_id":    session.SessionID,
		"chat_name":     chatName,
		"description":   chatCfg.Desc,
		"message":       message,
		"message_count": msgCount,
	})
}

// handleChat handles chat messages. The target chat is taken from the request
// (chat_name), falling back to the connection's current chat for legacy
// clients. At most one stream runs per chat and a session-wide cap limits how
// many chats may stream concurrently.
func (h *WebSocketHandler) handleChat(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	if chatName == "" {
		h.sendChatError(session, "", "Please select a chat first")
		return
	}

	cc := session.GetChat(chatName)
	if cc == nil || cc.ChatSession == nil || cc.ChatBot == nil {
		h.sendChatError(session, chatName, fmt.Sprintf("Chat '%s' is not open on this connection", chatName))
		return
	}

	// Enforce the session-wide concurrent stream cap
	if !h.sessionManager.tryStartStream(session.SessionID) {
		h.sendChatError(session, chatName, fmt.Sprintf("Too many concurrent responses in this session (max %d), please wait for one to finish", maxConcurrentStreamsPerSession))
		return
	}
	defer h.sessionManager.stopStream(session.SessionID)

	// Enforce one in-flight stream per chat
	if !cc.InFlight().CompareAndSwap(false, true) {
		h.sendChatError(session, chatName, fmt.Sprintf("Chat '%s' is busy with another response", chatName))
		return
	}
	defer cc.InFlight().Store(false)

	// Reset cancel state for new request (per chat)
	cc.ResetCancel()

	// Create a cancellable context
	ctx, cancelFunc := context.WithCancel(context.Background())
	cc.SetCancelFunc(cancelFunc)

	// Convert FilePayload to FileData
	var fileData []chatbot.FileData
	if len(req.Files) > 0 {
		fileData = make([]chatbot.FileData, len(req.Files))
		for i, file := range req.Files {
			fileData[i] = chatbot.FileData{
				URL:      file.URL,
				Type:     file.Type,
				Name:     file.Name,
				FileSize: file.FileSize,
			}
		}
	}

	// Use pre-initialized ChatBot to process message with files
	err := cc.ChatBot.StreamChatWithHandler(ctx, req.Message, fileData)
	if err != nil && !cc.IsCancelled() {
		// The error message has already been sent by StreamChatWithHandler via
		// the handler; only handle side effects here (MCP reinit).
		if strings.Contains(err.Error(), "failed to call mcp tool") && strings.Contains(err.Error(), "transport error") {
			ctx := context.Background()
			newChatSession, err := chatbot.InitChatSession(ctx, h.cfg, chatName, session.SessionID, false)
			if err != nil {
				h.sendChatError(session, chatName, fmt.Sprintf("Failed to initialize chat session: %v", err))
				return
			}
			cc.ChatSession.Close()
			newChatSession.Manager.SetChatModel(newChatSession.Manager.GetChatModel())
			cb := chatbot.NewChatBot(ctx, newChatSession.Agent, newChatSession.Manager, nil, newChatSession.PersistenceStore())
			cc.ChatSession = newChatSession
			cc.ChatBot = &cb
			cb.SetHandler(cc.WSHandler)
			h.sessionManager.UpdateChatSessionWithBot(session.SessionID, chatName, newChatSession, &cb)
			h.sendChatError(session, chatName, "Reinit chat session for refresh mcp client")
		}
		return
	}

	// If cancelled, send stopped message
	if cc.IsCancelled() {
		session.SendMessage("stopped", map[string]interface{}{
			"chat_name": chatName,
			"message":   "Response stopped by user",
		})
	}
}

// handleClear handles clear context request for a specific chat
func (h *WebSocketHandler) handleClear(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	cc := session.GetChat(chatName)
	if cc == nil || cc.ChatSession == nil {
		h.sendChatError(session, chatName, "No active chat to clear")
		return
	}

	// Clear conversation record for the target chat only
	cc.ChatSession.Clear()
	// Get updated message count (should be 0 after clear)
	msgCount := cc.ChatSession.GetMessageCount()
	session.SendMessage("cleared", map[string]interface{}{
		"chat_name":     chatName,
		"message":       fmt.Sprintf("Conversation context cleared for chat: %s", chatName),
		"message_count": msgCount,
	})
}

// handleKeep handles keep session request (execute keep hook) for a specific chat
func (h *WebSocketHandler) handleKeep(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	cc := session.GetChat(chatName)
	if cc == nil || cc.ChatSession == nil {
		h.sendChatError(session, chatName, "No active chat to keep")
		return
	}

	if err := cc.ChatSession.OnKeep(); err != nil {
		log.Printf("Session %s: Keep hook failed for chat %s: %v", session.SessionID, chatName, err)
		session.SendMessage("kept", map[string]interface{}{
			"chat_name": chatName,
			"message":   fmt.Sprintf("Keep hook executed with error: %v", err),
		})
	} else {
		log.Printf("Session %s: Keep hook executed successfully for chat %s", session.SessionID, chatName)
		session.SendMessage("kept", map[string]interface{}{
			"chat_name": chatName,
			"message":   "Session keep hook executed successfully",
		})
	}
}

// handleStop handles stop request for the ongoing chat of a specific chat
func (h *WebSocketHandler) handleStop(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	cc := session.GetChat(chatName)
	if cc == nil {
		return
	}
	log.Printf("Session %s: Stop requested for chat %s", session.SessionID, chatName)

	// Set cancelled flag to stop ongoing stream (per chat)
	cc.SetCancelled()
}

// handleDeselectChat handles closing a chat tab on this connection. The chat
// state is kept in the session for later restoration; its in-flight stream
// (if any) is stopped.
func (h *WebSocketHandler) handleDeselectChat(session *chatbot.WSSession, req *ChatRequest) {
	chatName := req.ChatName
	if chatName == "" {
		return
	}
	if cc := session.GetChat(chatName); cc != nil {
		// Stop an in-flight stream for this chat (the stream goroutine will
		// send the "stopped" event; it is dropped if the client closed the tab).
		cc.SetCancelled()
	}
	log.Printf("Session %s: Deselecting chat '%s'", session.SessionID, chatName)
	session.DetachChat(chatName)
	h.sessionManager.markChatInactive(session.SessionID, chatName)
	if session.CurrentChat == chatName {
		session.CurrentChat = ""
	}
}

// handleApprovalResponse handles approval response from the client
func (h *WebSocketHandler) handleApprovalResponse(session *chatbot.WSSession, msg *chatbot.WSMessage) {
	var payload ApprovalResponsePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		log.Printf("Invalid approval_response format: %v", err)
		session.SendError("Invalid approval_response format")
		return
	}

	log.Printf("Session %s: Processing approval_response for approval %s with %d results",
		session.SessionID, payload.ApprovalID, len(payload.Results))

	// Convert results to ApprovalResultMap
	results := make(chatbot.ApprovalResultMap, len(payload.Results))
	for id, item := range payload.Results {
		result := &mcp.ApprovalResult{
			Approved: item.Approved,
		}
		if item.Reason != "" {
			result.DisapproveReason = &item.Reason
		}
		results[id] = result
	}

	// Pass the response to the session
	session.HandleApprovalResponse(payload.ApprovalID, results)
}

func init() {
	// Add serve command
	serveCmd.Flags().StringP("host", "", "0.0.0.0", "Host to listen on")
	serveCmd.Flags().IntP("port", "", 8080, "Port to listen on")
	serveCmd.Flags().StringP("basic-auth", "", "", "Basic auth credentials as comma-separated user:pass pairs (e.g., \"alice:pwd1,bob:pwd2\")")
	serveCmd.Flags().StringP("basic-auth-file", "", "", "Path to a file containing user:password pairs (one per line, # for comments)")

	RootCmd.AddCommand(serveCmd)
}
