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

// BasicAuthMiddleware creates a middleware for HTTP Basic Authentication
func BasicAuthMiddleware(user, pass string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth if credentials are not configured
			if user == "" && pass == "" {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
				return
			}

			// Extract credentials from Authorization header
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
				return
			}

			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
				return
			}

			credentialParts := strings.SplitN(string(decoded), ":", 2)
			if len(credentialParts) != 2 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
				return
			}

			receivedUser := credentialParts[0]
			receivedPass := credentialParts[1]

			if receivedUser != user || receivedPass != pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("401 Unauthorized"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start chat-agent in web mode with WebSocket support",
	Long: `Start chat-agent as a WebSocket server for web-based chat interactions.

Each client connection can select a chat and start independent conversation sessions.

Example:
  chat-agent serve --port 8080`,
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
		basicAuthUser, _ := cmd.Flags().GetString("basic-auth-user")
		basicAuthPass, _ := cmd.Flags().GetString("basic-auth-pass")

		wsHandler := NewWebSocketHandler(cfg)

		authMiddleware := BasicAuthMiddleware(basicAuthUser, basicAuthPass)

		router := mux.NewRouter()
		router.Use(authMiddleware)
		router.HandleFunc("/ws", wsHandler.HandleWebSocket)

		router.HandleFunc("/chats", func(w http.ResponseWriter, r *http.Request) {
			chats := make([]string, 0, len(cfg.Chats))
			defaultChat := ""
			for name, chatCfg := range cfg.Chats {
				chats = append(chats, name)
				if chatCfg.Default {
					defaultChat = name
				}
			}
			sort.Strings(chats)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"chats":        chats,
				"default_chat": defaultChat,
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
	// activeConnections tracks which sessions have active WebSocket connections
	activeConnections map[string]bool
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		sessions:          make(map[string]*SessionInfo),
		cfg:               cfg,
		activeConnections: make(map[string]bool),
	}
}

// tryRegisterConnection atomically checks and registers a connection.
// Returns true if registered successfully, false if session already has an active connection.
func (sm *SessionManager) tryRegisterConnection(sessionID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.activeConnections[sessionID] {
		return false
	}
	sm.activeConnections[sessionID] = true
	return true
}

// unregisterConnection marks a session as no longer having an active connection.
func (sm *SessionManager) unregisterConnection(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.activeConnections, sessionID)
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
		delete(sm.activeConnections, sessionID)
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

	// Check if there's already an active WebSocket connection for this session ID
	if !h.sessionManager.tryRegisterConnection(sessionID) {
		// Reject the connection - session is already in use
		log.Printf("WebSocket connection rejected for session %s: another connection is already active", sessionID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "Session already in use. Please close other tabs/windows using the same session.",
		})
		return
	}

	// Check if session already exists
	existingSession, exists := h.sessionManager.GetSession(sessionID)
	var session *chatbot.WSSession

	if exists && len(existingSession.Chats) > 0 {
		// Reuse existing session - create new WSSession with same ID but new connection
		session = chatbot.NewWSSession(conn, sessionID, h.cfg)
		// Restore current chat name
		session.ChatName = existingSession.ChatName

		// Restore chat session and bot for the current chat
		if session.ChatName != "" {
			if chatState, ok := existingSession.Chats[session.ChatName]; ok && chatState.ChatSession != nil {
				session.ChatSession = chatState.ChatSession
				session.ChatBot = chatState.ChatBot
				// Reinitialize WSHandler with new connection
				session.WSHandler = chatbot.NewWSChatHandler(session)
				// Update ChatBot's handler to use the new WSHandler
				if session.ChatBot != nil {
					session.ChatBot.SetHandler(session.WSHandler)
				}
				log.Printf("Reconnected to existing session %s (chat: %s)", sessionID, session.ChatName)
			}
		}
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

	// Ensure cleanup on connection close
	defer func() {
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

		go h.processMessage(session, &wsMsg)
	}
}

// processMessage processes a WebSocket message
func (h *WebSocketHandler) processMessage(session *chatbot.WSSession, msg *chatbot.WSMessage) {
	switch msg.Type {
	case "select_chat":
		h.handleSelectChat(session, msg)
	case "chat":
		h.handleChat(session, msg)
	case "stop":
		h.handleStop(session)
	case "clear":
		h.handleClear(session)
	case "keep":
		h.handleKeep(session)
	case "approval_response":
		h.handleApprovalResponse(session, msg)
	default:
		session.SendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
	}
}

// handleSelectChat handles chat selection
func (h *WebSocketHandler) handleSelectChat(session *chatbot.WSSession, msg *chatbot.WSMessage) {
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
		log.Printf("Session %s: Reactivating existing chat session for '%s'", session.SessionID, req.ChatName)
		// Reinitialize WSHandler with current connection
		session.WSHandler = chatbot.NewWSChatHandler(session)
		if session.ChatBot != nil {
			session.ChatBot.SetHandler(session.WSHandler)
		}
		// Update session manager
		h.sessionManager.UpdateChatSessionWithBot(session.SessionID, req.ChatName, session.ChatSession, session.ChatBot)
		session.SendMessage("chat_selected", map[string]interface{}{
			"session_id":  session.SessionID,
			"chat_name":   req.ChatName,
			"description": chatCfg.Desc,
			"message":     fmt.Sprintf("Reactivated chat: %s", req.ChatName),
		})
		return
	}

	// Switching to a different chat
	previousChat := session.ChatName
	if previousChat != "" {
		log.Printf("Session %s: Switching chat from '%s' to '%s'", session.SessionID, previousChat, req.ChatName)
		// Note: We don't close the previous chat session, just save its state
		// The MCP client and other resources remain alive in the saved ChatState
		session.ChatSession = nil
		session.ChatBot = nil
		session.WSHandler = nil
	}

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
		session.SendMessage("chat_selected", map[string]interface{}{
			"session_id":  session.SessionID,
			"chat_name":   req.ChatName,
			"description": chatCfg.Desc,
			"message":     fmt.Sprintf("Restored chat: %s", req.ChatName),
		})
		return
	}

	// Initialize new chat session
	ctx := context.Background()
	chatSession, err := chatbot.InitChatSession(ctx, h.cfg, req.ChatName, session.SessionID, false)
	if err != nil {
		session.SendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
		return
	}

	// Initialize ChatBot
	cb := chatbot.NewChatBot(ctx, chatSession.Agent, chatSession.Manager, nil)
	wsHandler := chatbot.NewWSChatHandler(session)
	cb.SetHandler(wsHandler)

	// Save chat session and bot
	session.ChatName = req.ChatName
	session.ChatSession = chatSession
	session.ChatBot = &cb
	session.WSHandler = wsHandler

	// Update session manager with chat session and bot
	h.sessionManager.UpdateChatSessionWithBot(session.SessionID, req.ChatName, chatSession, &cb)

	session.SendMessage("chat_selected", map[string]interface{}{
		"chat_name":   req.ChatName,
		"description": chatCfg.Desc,
		"message":     fmt.Sprintf("Selected chat: %s", req.ChatName),
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
		session.SendMessage("cleared", map[string]interface{}{
			"chat_name": session.ChatName,
			"message":   fmt.Sprintf("Conversation context cleared for chat: %s", session.ChatName),
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

	// Send stopped message to client
	session.SendMessage("stopped", map[string]interface{}{
		"message": "Response stopped by user",
	})
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
	serveCmd.Flags().StringP("basic-auth-user", "", "", "Basic auth username (enables authentication if set)")
	serveCmd.Flags().StringP("basic-auth-pass", "", "", "Basic auth password")

	RootCmd.AddCommand(serveCmd)
}
