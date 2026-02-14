package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/store"
	"github.com/Arvintian/chat-agent/pkg/utils"
	"github.com/Arvintian/chat-agent/pkg/web"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

// WebSocket message types
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ChatRequest struct {
	ChatName string `json:"chat_name"`
	Message  string `json:"message"`
}

type ChatResponse struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

type SessionInfo struct {
	ID        string
	ChatName  string
	CreatedAt time.Time
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

// WSSession represents a WebSocket session with its connection
type WSSession struct {
	conn        *websocket.Conn
	sessionID   string
	cfg         *config.Config
	cleanupReg  *utils.CleanupRegistry
	chatName    string       // 当前选中的 chat 名称
	chatSession *ChatSession // 已初始化的 chat session，复用
}

func NewWSSession(conn *websocket.Conn, sessionID string, cfg *config.Config, cleanupReg *utils.CleanupRegistry) *WSSession {
	return &WSSession{
		conn:        conn,
		sessionID:   sessionID,
		cfg:         cfg,
		cleanupReg:  cleanupReg,
		chatName:    "",
		chatSession: nil,
	}
}

func (s *WSSession) sendMessage(msgType string, content interface{}) {
	data := WSMessage{Type: msgType}
	payload, _ := json.Marshal(content)
	data.Payload = payload
	if err := s.conn.WriteJSON(data); err != nil {
		log.Printf("Error sending message to session %s: %v", s.sessionID, err)
	}
}

func (s *WSSession) sendError(errMsg string) {
	s.sendMessage("error", ChatResponse{Error: errMsg})
}

func (s *WSSession) sendChunk(content string, isFirst, isLast bool) {
	s.sendMessage("chunk", map[string]interface{}{
		"content": content,
		"first":   isFirst,
		"last":    isLast,
	})
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

		// Create session manager
		sessionManager := NewSessionManager(cfg)

		// Setup HTTP routes
		http.Handle("/ws", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleWebSocket(w, r, sessionManager, cleanupRegistry, cfg)
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

func init() {
	// Add serve command
	serveCmd.Flags().StringP("host", "", "0.0.0.0", "Host to listen on")
	serveCmd.Flags().IntP("port", "", 8080, "Port to listen on")

	RootCmd.AddCommand(serveCmd)
}

// handleWebSocket handles WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request, sessionManager *SessionManager, cleanupRegistry *utils.CleanupRegistry, cfg *config.Config) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Each connection is a new session - no session persistence needed
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	log.Printf("WebSocket connection: %s", sessionID)

	session := NewWSSession(conn, sessionID, cfg, cleanupRegistry)

	// Handle messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error for session %s: %v", sessionID, err)
			}
			break
		}

		var wsMsg WSMessage
		if err := json.Unmarshal(message, &wsMsg); err != nil {
			log.Printf("Invalid message format: %v", err)
			session.sendError("Invalid message format")
			continue
		}

		processWebSocketMessage(session, sessionManager, &wsMsg)
	}

	// 清理 chat session
	if session.chatSession != nil {
		session.chatSession = nil
	}

	sessionManager.RemoveSession(sessionID)
	log.Printf("Session %s closed", sessionID)
}

func processWebSocketMessage(session *WSSession, sessionManager *SessionManager, msg *WSMessage) {
	switch msg.Type {
	case "select_chat":
		handleSelectChat(session, msg, sessionManager)
	case "chat":
		handleChat(session, msg)
	case "clear":
		handleClear(session)
	default:
		session.sendError(fmt.Sprintf("Unknown message type: %s", msg.Type))
	}
}

func handleSelectChat(session *WSSession, msg *WSMessage, sessionManager *SessionManager) {
	var req ChatRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		session.sendError("Invalid select_chat request")
		return
	}

	// Verify chat exists
	chatCfg, ok := session.cfg.Chats[req.ChatName]
	if !ok {
		session.sendError(fmt.Sprintf("Chat '%s' not found", req.ChatName))
		return
	}

	// 如果已初始化相同的 chat，直接返回
	if session.chatName == req.ChatName && session.chatSession != nil {
		session.sendMessage("chat_selected", map[string]interface{}{
			"session_id": session.sessionID,
			"chat_name":  req.ChatName,
			"message":    fmt.Sprintf("Already selected chat: %s", req.ChatName),
		})
		return
	}

	// 如果已有不同的 chat session，先清理
	if session.chatSession != nil {
		session.chatSession = nil
	}

	// 初始化 chat session
	ctx := context.Background()
	chatSession, err := initChatSession(ctx, session.cfg, session.cleanupReg, req.ChatName, false)
	if err != nil {
		session.sendError(fmt.Sprintf("Failed to initialize chat session: %v", err))
		return
	}

	// 保存 chat session
	session.chatName = req.ChatName
	session.chatSession = chatSession

	session.sendMessage("chat_selected", map[string]interface{}{
		"chat_name":   req.ChatName,
		"description": chatCfg.Desc,
		"message":     fmt.Sprintf("Selected chat: %s", req.ChatName),
	})
}

func handleChat(session *WSSession, msg *WSMessage) {
	var req ChatRequest
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		session.sendError("Invalid chat request")
		return
	}

	// 检查是否已选择 chat 并已初始化 session
	if session.chatName == "" || session.chatSession == nil {
		session.sendError("Please select a chat first")
		return
	}

	// 发送 thinking indicator
	session.sendMessage("thinking", map[string]interface{}{"status": true})

	// 直接使用已初始化的 session 处理消息
	ctx := context.Background()
	err := processChatMessage(ctx, session, session.chatSession, req.Message)
	if err != nil {
		session.sendError(err.Error())
		return
	}

	session.sendMessage("complete", map[string]interface{}{
		"message": "Response completed",
	})
}

func handleClear(session *WSSession) {
	// 清除对应 session 的对话记录
	if session.chatSession != nil {
		session.chatSession.Manager.Clear()
		session.sendMessage("cleared", map[string]interface{}{
			"chat_name": session.chatName,
			"message":   "Conversation context cleared",
		})
	} else {
		session.sendMessage("cleared", map[string]interface{}{
			"message": "No active session to clear",
		})
	}
}

// processChatMessage handles the chat processing
func processChatMessage(ctx context.Context, session *WSSession, chatSession *ChatSession, userInput string) error {
	// Add user message to manager
	chatSession.Manager.AddMessage(schema.UserMessage(userInput))

	// Create runner and run chat
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatSession.Agent,
		EnableStreaming: true,
		CheckPointStore: store.NewInMemoryStore(),
	})

	messages := chatSession.Manager.GetMessages()
	streamReader := runner.Run(ctx, messages, adk.WithCheckPointID("web"))

	response := ""
	firstChunk := true

	for {
		event, ok := streamReader.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return event.Err
		}

		if event.Output == nil {
			continue
		}

		if event.Output.MessageOutput.Role == schema.Tool {
			session.sendChunk(fmt.Sprintf("[Tool: %s]", event.Output.MessageOutput.ToolName), firstChunk, false)
			firstChunk = false
			continue
		}

		if event.Output.MessageOutput.MessageStream != nil {
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error receiving message stream: %w", err)
				}

				if message.Content != "" {
					response += message.Content
					session.sendChunk(message.Content, firstChunk, false)
					firstChunk = false
				}
			}
		} else if event.Output.MessageOutput.Message != nil {
			if len(event.Output.MessageOutput.Message.ToolCalls) > 0 {
				for _, tc := range event.Output.MessageOutput.Message.ToolCalls {
					toolInfo := tc.Function.Name
					if strings.Contains(tc.Function.Arguments, "name") {
						// Try to extract tool name from arguments
						toolInfo = fmt.Sprintf("%s(%s)", tc.Function.Name, tc.Function.Arguments)
					}
					session.sendChunk(fmt.Sprintf("[Tool: %s]", toolInfo), firstChunk, false)
					firstChunk = false
				}
			}
			if event.Output.MessageOutput.Message.Content != "" {
				response += event.Output.MessageOutput.Message.Content
				session.sendChunk(event.Output.MessageOutput.Message.Content, firstChunk, false)
				firstChunk = false
			}
		}
	}

	// Send final chunk marker
	session.sendChunk("", false, true)

	// Add assistant message to manager
	chatSession.Manager.AddMessage(schema.AssistantMessage(response, nil))

	return nil
}
