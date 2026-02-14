package chatbot

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/utils"

	"github.com/gorilla/websocket"
)

// WebSocket message types
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// WSSession represents a WebSocket session with its connection
type WSSession struct {
	conn        *websocket.Conn
	cfg         *config.Config
	cleanupReg  *utils.CleanupRegistry
	SessionID   string
	ChatName    string
	ChatSession *ChatSession
	ChatBot     *ChatBot
	WSHandler   *WSChatHandler
}

func NewWSSession(conn *websocket.Conn, sessionID string, cfg *config.Config, cleanupReg *utils.CleanupRegistry) *WSSession {
	return &WSSession{
		conn:        conn,
		cfg:         cfg,
		cleanupReg:  cleanupReg,
		SessionID:   sessionID,
		ChatName:    "",
		ChatSession: nil,
		ChatBot:     nil,
		WSHandler:   nil,
	}
}

func (s *WSSession) SendMessage(msgType string, content interface{}) {
	data := WSMessage{Type: msgType}
	payload, _ := json.Marshal(content)
	data.Payload = payload
	if err := s.conn.WriteJSON(data); err != nil {
		log.Printf("Error sending message to session %s: %v", s.SessionID, err)
	}
}

func (s *WSSession) SendChunk(content string, isFirst, isLast bool) {
	s.SendMessage("chunk", map[string]interface{}{
		"content": content,
		"first":   isFirst,
		"last":    isLast,
	})
}

func (s *WSSession) SendError(errMsg string) {
	s.SendMessage("error", map[string]string{"error": errMsg})
}

// WSChatHandler implements Handler for WebSocket output
type WSChatHandler struct {
	session *WSSession
}

func NewWSChatHandler(session *WSSession) *WSChatHandler {
	return &WSChatHandler{session: session}
}

func (h *WSChatHandler) SendChunk(content string, first, last bool) {
	h.session.SendChunk(content, first, last)
}

func (h *WSChatHandler) SendToolCall(name string) {
	h.session.SendChunk(fmt.Sprintf("[Tool: %s]", name), false, false)
}

func (h *WSChatHandler) SendThinking(status bool) {
	h.session.SendMessage("thinking", map[string]interface{}{"status": status})
}

func (h *WSChatHandler) SendComplete(message string) {
	h.session.SendMessage("complete", map[string]interface{}{"message": message})
}

func (h *WSChatHandler) SendError(err string) {
	h.session.SendError(err)
}
