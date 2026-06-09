package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

// BasicAuthMiddleware creates a middleware for HTTP Basic Authentication.
// It accepts a map of username->password pairs and authenticates against any of them.
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

			next.ServeHTTP(w, r)
		})
	}
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

		router.PathPrefix("/").Handler(http.FileServer(web.GetFS()))

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
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		sessions:        make(map[string]*SessionInfo),
		cfg:             cfg,
		connectionCount: make(map[string]int),
		activeChats:     make(map[string]map[string]int),
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

	// Track the chat that this connection has active
	connectionActiveChat := ""

	// Check if session already exists
	existingSession, exists := h.sessionManager.GetSession(sessionID)
	var session *chatbot.WSSession

	if exists && len(existingSession.Chats) > 0 {
		// Reuse existing session - create new WSSession with same ID but new connection
		// Don't auto-restore any chat - let the client explicitly select one.
		// This prevents conflicts when multiple tabs share a session.
		session = chatbot.NewWSSession(conn, sessionID, h.cfg)
		log.Printf("Reconnected to existing session %s with %d chats", sessionID, len(existingSession.Chats))
	} else {
		// Create new session
		session = chatbot.NewWSSession(conn, sessionID, h.cfg)
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
		// Mark chat inactive if this connection had one active
		if connectionActiveChat != "" {
			h.sessionManager.markChatInactive(sessionID, connectionActiveChat)
		}
		// Cleanup handler and logging
		if session.ChatSession != nil {
			session.WSHandler = nil
			log.Printf("Session %s disconnected (kept in memory, chat: %s)", sessionID, session.ChatName)
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

		go h.processMessage(session, &wsMsg, &connectionActiveChat)
	}
}

// processMessage processes a WebSocket message
func (h *WebSocketHandler) processMessage(session *chatbot.WSSession, msg *chatbot.WSMessage, connectionActiveChat *string) {
	switch msg.Type {
	case "select_chat":
		h.handleSelectChat(session, msg, connectionActiveChat)
	case "chat":
		h.handleChat(session, msg)
	case "regenerate":
		// Remove last round (user message + assistant response) before re-processing
		if session.ChatSession != nil {
			session.ChatSession.RemoveLastRound()
		}
		// Then process as normal chat
		h.handleChat(session, msg)
	case "stop":
		h.handleStop(session)
	case "clear":
		h.handleClear(session)
	case "keep":
		h.handleKeep(session)
	case "approval_response":
		h.handleApprovalResponse(session, msg)
	case "deselect_chat":
		h.handleDeselectChat(session, connectionActiveChat)
	default:
		session.SendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
	}
}

// handleSelectChat handles chat selection
func (h *WebSocketHandler) handleSelectChat(session *chatbot.WSSession, msg *chatbot.WSMessage, connectionActiveChat *string) {
	var req ChatRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		session.SendError("Invalid select_chat request")
		return
	}

	// Verify chat exists
	chatCfg, ok := h.cfg.Chats[req.ChatName]
	if !ok {
		session.SendError(fmt.Sprintf("Chat '%s' not found", req.ChatName))
		return
	}

	// If already using the same chat and it's initialized, just reinitialize the WSHandler
	if session.ChatName == req.ChatName && session.ChatSession != nil {
		// Same chat on same connection - just reinitialize handler
		// Mark as active if not already tracked
		if *connectionActiveChat != req.ChatName {
			if *connectionActiveChat != "" {
				h.sessionManager.markChatInactive(session.SessionID, *connectionActiveChat)
			}
			h.sessionManager.markChatActive(session.SessionID, req.ChatName)
			*connectionActiveChat = req.ChatName
		}

		log.Printf("Session %s: Reactivating existing chat session for '%s'", session.SessionID, req.ChatName)
		// Reinitialize WSHandler with current connection
		session.WSHandler = chatbot.NewWSChatHandler(session)
		if session.ChatBot != nil {
			session.ChatBot.SetHandler(session.WSHandler)
		}
		// Update session manager
		h.sessionManager.UpdateChatSessionWithBot(session.SessionID, req.ChatName, session.ChatSession, session.ChatBot)

		// Get message count
		msgCount := session.ChatSession.GetMessageCount()

		session.SendMessage("chat_selected", map[string]interface{}{
			"session_id":    session.SessionID,
			"chat_name":     req.ChatName,
			"description":   chatCfg.Desc,
			"message":       fmt.Sprintf("Reactivated chat: %s", req.ChatName),
			"message_count": msgCount,
		})
		return
	}

	// Check if this chat is already active in another connection of the same session.
	// The 5s ping/pong mechanism ensures dead connections are cleaned up quickly.
	if h.sessionManager.isChatActive(session.SessionID, req.ChatName) {
		session.SendError(fmt.Sprintf("Chat '%s' is already active in another connection of this session", req.ChatName))
		return
	}

	// Switching to a different chat
	previousChat := session.ChatName
	if previousChat != "" {
		log.Printf("Session %s: Switching chat from '%s' to '%s'", session.SessionID, previousChat, req.ChatName)
		// Mark old chat as inactive
		h.sessionManager.markChatInactive(session.SessionID, previousChat)
		// Note: We don't close the previous chat session, just save its state
		// The MCP client and other resources remain alive in the saved ChatState
		session.ChatSession = nil
		session.ChatBot = nil
		session.WSHandler = nil
	}

	// Mark new chat as active
	h.sessionManager.markChatActive(session.SessionID, req.ChatName)
	*connectionActiveChat = req.ChatName

	// Check if this chat was previously used in this session (restore state)
	if chatState, ok := h.sessionManager.GetChatState(session.SessionID, req.ChatName); ok && chatState.ChatSession != nil {
		log.Printf("Session %s: Restoring existing chat session for '%s'", session.SessionID, req.ChatName)
		// Restore the saved chat state
		session.ChatName = req.ChatName
		session.ChatSession = chatState.ChatSession
		session.ChatBot = chatState.ChatBot
		// Reinitialize WSHandler with current connection
		session.WSHandler = chatbot.NewWSChatHandler(session)
		if session.ChatBot != nil {
			session.ChatBot.SetHandler(session.WSHandler)
		}
		// Update session manager with current active chat
		h.sessionManager.UpdateChatSessionWithBot(session.SessionID, req.ChatName, session.ChatSession, session.ChatBot)

		// Get message count
		msgCount := session.ChatSession.GetMessageCount()

		session.SendMessage("chat_selected", map[string]interface{}{
			"session_id":    session.SessionID,
			"chat_name":     req.ChatName,
			"description":   chatCfg.Desc,
			"message":       fmt.Sprintf("Restored chat: %s", req.ChatName),
			"message_count": msgCount,
		})
		return
	}

	// Initialize new chat session
	ctx := context.Background()
	chatSession, err := chatbot.InitChatSession(ctx, h.cfg, req.ChatName, session.SessionID, false)
	if err != nil {
		// Clean up active chat tracking on failure
		h.sessionManager.markChatInactive(session.SessionID, req.ChatName)
		*connectionActiveChat = ""
		session.SendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
		return
	}

	// Initialize ChatBot with persistence store
	cb := chatbot.NewChatBot(ctx, chatSession.Agent, chatSession.Manager, nil, chatSession.PersistenceStore())
	wsHandler := chatbot.NewWSChatHandler(session)
	cb.SetHandler(wsHandler)

	// Save chat session and bot
	session.ChatName = req.ChatName
	session.ChatSession = chatSession
	session.ChatBot = &cb
	session.WSHandler = wsHandler

	// Update session manager with chat session and bot
	h.sessionManager.UpdateChatSessionWithBot(session.SessionID, req.ChatName, chatSession, &cb)

	// Get message count
	msgCount := chatSession.GetMessageCount()

	session.SendMessage("chat_selected", map[string]interface{}{
		"session_id":    session.SessionID,
		"chat_name":     req.ChatName,
		"description":   chatCfg.Desc,
		"message":       fmt.Sprintf("Selected chat: %s", req.ChatName),
		"message_count": msgCount,
	})
}

// handleChat handles chat messages
func (h *WebSocketHandler) handleChat(session *chatbot.WSSession, msg *chatbot.WSMessage) {
	var req ChatRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		session.SendError("Invalid chat request")
		return
	}

	// Check if chat is selected and session is initialized
	if session.ChatName == "" || session.ChatSession == nil || session.WSHandler == nil {
		session.SendError("Please select a chat first")
		return
	}

	// Reset cancel state for new request
	session.ResetCancel()

	// Create a cancellable context
	ctx, cancelFunc := context.WithCancel(context.Background())
	session.SetCancelFunc(cancelFunc)

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
	err := session.ChatBot.StreamChatWithHandler(ctx, req.Message, fileData)
	if err != nil && !session.IsCancelled() {
		session.SendError(err.Error())
		if strings.Contains(err.Error(), "failed to call mcp tool") && strings.Contains(err.Error(), "transport error") {
			ctx := context.Background()
			chatSession, err := chatbot.InitChatSession(ctx, h.cfg, session.ChatName, session.SessionID, false)
			if err != nil {
				session.SendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
				return
			}
			session.ChatSession.Close()
			session.ChatSession.Manager.SetChatModel(chatSession.Manager.GetChatModel())
			cb := chatbot.NewChatBot(ctx, chatSession.Agent, session.ChatSession.Manager, nil, chatSession.PersistenceStore())
			cb.SetHandler(session.WSHandler)
			session.ChatSession = chatSession
			session.ChatBot = &cb
			session.SendError("Reinit chat session for refresh mcp client")
		}
		return
	}

	// If cancelled, send stopped message
	if session.IsCancelled() {
		session.SendMessage("stopped", map[string]interface{}{
			"message": "Response stopped by user",
		})
	}
}

// handleClear handles clear context request
func (h *WebSocketHandler) handleClear(session *chatbot.WSSession) {
	// Clear conversation record for the current chat only
	if session.ChatSession != nil {
		session.ChatSession.Clear()
		// Get updated message count (should be 0 after clear)
		msgCount := session.ChatSession.GetMessageCount()
		session.SendMessage("cleared", map[string]interface{}{
			"chat_name":     session.ChatName,
			"message":       fmt.Sprintf("Conversation context cleared for chat: %s", session.ChatName),
			"message_count": msgCount,
		})
	} else {
		session.SendMessage("cleared", map[string]interface{}{
			"message": "No active session to clear",
		})
	}
}

// handleKeep handles keep session request (execute keep hook)
func (h *WebSocketHandler) handleKeep(session *chatbot.WSSession) {
	if session.ChatSession != nil {
		if err := session.ChatSession.OnKeep(); err != nil {
			log.Printf("Session %s: Keep hook failed: %v", session.SessionID, err)
			session.SendMessage("kept", map[string]interface{}{
				"chat_name": session.ChatName,
				"message":   fmt.Sprintf("Keep hook executed with error: %v", err),
			})
		} else {
			log.Printf("Session %s: Keep hook executed successfully", session.SessionID)
			session.SendMessage("kept", map[string]interface{}{
				"chat_name": session.ChatName,
				"message":   "Session keep hook executed successfully",
			})
		}
	} else {
		session.SendMessage("kept", map[string]interface{}{
			"message": "No active session to keep",
		})
	}
}

// handleStop handles stop request for ongoing chat
func (h *WebSocketHandler) handleStop(session *chatbot.WSSession) {
	log.Printf("Session %s: Stop requested", session.SessionID)

	// Set cancelled flag to stop ongoing stream
	session.SetCancelled()
}

// handleDeselectChat handles deselecting the current chat (user returns to selection page)
func (h *WebSocketHandler) handleDeselectChat(session *chatbot.WSSession, connectionActiveChat *string) {
	if *connectionActiveChat != "" {
		log.Printf("Session %s: Deselecting chat '%s'", session.SessionID, *connectionActiveChat)
		h.sessionManager.markChatInactive(session.SessionID, *connectionActiveChat)
		*connectionActiveChat = ""
	}
	// Clear the chat state from the WSSession but keep it in SessionInfo for later restoration
	session.ChatName = ""
	session.ChatSession = nil
	session.ChatBot = nil
	session.WSHandler = nil
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
