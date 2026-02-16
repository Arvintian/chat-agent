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

		wsHandler.CloseAllSessions()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}

		log.Printf("Server stopped")
		return nil
	},
}

type ChatRequest struct {
	ChatName string `json:"chat_name"`
	Message  string `json:"message"`
}

type SessionInfo struct {
	ID          string
	ChatName    string
	ChatSession *chatbot.ChatSession
	CreatedAt   time.Time
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
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*SessionInfo),
		cfg:      cfg,
	}
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
		sm.sessions[sessionID] = &SessionInfo{
			ID:          sessionID,
			ChatName:    chatName,
			ChatSession: chatSession,
			CreatedAt:   time.Now(),
		}
	}
}

func (sm *SessionManager) UpdateChatSession(sessionID string, chatName string, chatSession *chatbot.ChatSession) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[sessionID]; ok {
		session.ChatName = chatName
		session.ChatSession = chatSession
	}
}

func (sm *SessionManager) RemoveSession(sessionID string) *chatbot.ChatSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil
	}
	delete(sm.sessions, sessionID)
	return session.ChatSession
}

func (sm *SessionManager) CloseAllSessions() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for sessionID, session := range sm.sessions {
		if session.ChatSession != nil {
			if err := session.ChatSession.Close(); err != nil {
				log.Printf("Error closing session %s: %v", sessionID, err)
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

	// Each connection is a new session - no session persistence needed
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	log.Printf("WebSocket connection: %s", sessionID)

	session := chatbot.NewWSSession(conn, sessionID, h.cfg)
	h.sessionManager.AddSession(sessionID, "", nil)

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

	// Cleanup chat session
	if session.ChatSession != nil {
		if err := session.ChatSession.Close(); err != nil {
			log.Printf("Error closing chat session: %v", err)
		}
		session.ChatSession = nil
	}

	h.sessionManager.RemoveSession(sessionID)
	log.Printf("Session %s closed", sessionID)
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

	// If already initialized the same chat, return
	if session.ChatName == req.ChatName && session.ChatSession != nil && session.WSHandler != nil {
		session.SendMessage("chat_selected", map[string]interface{}{
			"session_id": session.SessionID,
			"chat_name":  req.ChatName,
			"message":    fmt.Sprintf("Already selected chat: %s", req.ChatName),
		})
		return
	}

	// If a different chat session exists, cleanup first
	if session.ChatSession != nil {
		if err := session.ChatSession.Close(); err != nil {
			log.Printf("Error closing previous chat session: %v", err)
		}
		session.ChatSession = nil
	}

	// Initialize chat session
	ctx := context.Background()
	chatSession, err := chatbot.InitChatSession(ctx, h.cfg, req.ChatName, false)
	if err != nil {
		session.SendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
		return
	}

	// Update session info with new chat session
	h.sessionManager.UpdateChatSession(session.SessionID, req.ChatName, chatSession)

	// Initialize ChatBot for reuse
	cb := chatbot.NewChatBot(ctx, chatSession.Agent, chatSession.Manager, nil)
	wsHandler := chatbot.NewWSChatHandler(session)
	cb.SetHandler(wsHandler)

	// Save chat session and bot
	session.ChatName = req.ChatName
	session.ChatSession = chatSession
	session.ChatBot = &cb
	session.WSHandler = wsHandler

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

	// Use pre-initialized ChatBot to process message
	err := session.ChatBot.StreamChatWithHandler(ctx, req.Message)
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
	// Clear conversation record for the session
	if session.ChatSession != nil {
		session.ChatSession.Manager.Clear()
		session.SendMessage("cleared", map[string]interface{}{
			"chat_name": session.ChatName,
			"message":   "Conversation context cleared",
		})
	} else {
		session.SendMessage("cleared", map[string]interface{}{
			"message": "No active session to clear",
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
