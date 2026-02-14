package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/utils"
	"github.com/Arvintian/chat-agent/pkg/web"
	"github.com/gorilla/websocket"

	"github.com/spf13/cobra"
)

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
		cleanupRegistry := utils.NewCleanupRegistry()
		defer cleanupRegistry.Execute()

		// Load configuration file
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return err
		}

		port, _ := cmd.Flags().GetInt("port")
		host, _ := cmd.Flags().GetString("host")
		welcome, _ := cmd.Flags().GetString("welcome")

		// Create WebSocket handler
		wsHandler := NewWebSocketHandler(cfg, cleanupRegistry)

		// Setup HTTP routes
		http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wsHandler.HandleWebSocket(w, r)
		}))
		http.Handle("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("OK"))
		}))
		// Serve static files from embedded file system
		http.Handle("/", http.FileServer(web.GetFS()))
		http.HandleFunc("/chats", func(w http.ResponseWriter, r *http.Request) {
			// List available chats
			chats := make([]string, 0, len(cfg.Chats))
			defaultChat := ""
			for name, chatCfg := range cfg.Chats {
				chats = append(chats, name)
				if chatCfg.Default {
					defaultChat = name
				}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"chats":        chats,
				"default_chat": defaultChat,
			})
		})
		http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// Use --welcome as title, fallback to default
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

		addr := fmt.Sprintf("%s:%d", host, port)
		log.Printf("Starting chat-agent web server on %s", addr)
		log.Printf("WebSocket endpoint: ws://%s/ws", addr)
		log.Printf("HTTP endpoint: http://%s/", addr)

		return http.ListenAndServe(addr, nil)
	},
}

type ChatRequest struct {
	ChatName string `json:"chat_name"`
	Message  string `json:"message"`
}

type SessionInfo struct {
	ID        string
	ChatName  string
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
	mu       sync.RWMutex
	cfg      *config.Config
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

func (sm *SessionManager) AddSession(sessionID string, info *SessionInfo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[sessionID] = info
}

func (sm *SessionManager) RemoveSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// WebSocketHandler handles WebSocket connections
type WebSocketHandler struct {
	sessionManager *SessionManager
	cleanupReg     *utils.CleanupRegistry
	cfg            *config.Config
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(cfg *config.Config, cleanupReg *utils.CleanupRegistry) *WebSocketHandler {
	return &WebSocketHandler{
		sessionManager: NewSessionManager(cfg),
		cleanupReg:     cleanupReg,
		cfg:            cfg,
	}
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

	session := chatbot.NewWSSession(conn, sessionID, h.cfg, h.cleanupReg)
	h.sessionManager.AddSession(sessionID, &SessionInfo{
		ID:        sessionID,
		CreatedAt: time.Now(),
	})

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
		session.ChatSession = nil
	}

	// Initialize chat session
	ctx := context.Background()
	chatSession, err := chatbot.InitChatSession(ctx, h.cfg, h.cleanupReg, req.ChatName, false)
	if err != nil {
		session.SendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
		return
	}

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

	// Use pre-initialized ChatBot to process message
	ctx := context.Background()
	err := session.ChatBot.StreamChatWithHandler(ctx, req.Message)
	if err != nil {
		session.SendError(err.Error())
		return
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

	RootCmd.AddCommand(serveCmd)
}
