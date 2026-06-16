// Package serve provides a WebSocket client SDK for the chat-agent serve mode.
// It encapsulates the full WebSocket protocol: connection, chat selection,
// streaming responses, tool calls, thinking indicators, approval requests, and more.
package serve

import "encoding/json"

// Message types sent from server to client.
const (
	MsgSessionInit     = "session_init"
	MsgChatSelected    = "chat_selected"
	MsgChunk           = "chunk"
	MsgToolCall        = "tool_call"
	MsgThinking        = "thinking"
	MsgComplete        = "complete"
	MsgError           = "error"
	MsgApprovalRequest = "approval_request"
	MsgMessageCount    = "message_count"
	MsgStopped         = "stopped"
	MsgKept            = "kept"
	MsgCleared         = "cleared"
)

// Message types sent from client to server.
const (
	CmdSelectChat       = "select_chat"
	CmdChat             = "chat"
	CmdRegenerate       = "regenerate"
	CmdStop             = "stop"
	CmdClear            = "clear"
	CmdKeep             = "keep"
	CmdApprovalResponse = "approval_response"
	CmdDeselectChat     = "deselect_chat"
)

// WSMessage is the raw WebSocket message format used by the server protocol.
type WSMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// SessionInitPayload is received when a connection is first established.
type SessionInitPayload struct {
	SessionID string `json:"session_id"`
}

// ChatSelectedPayload is received after a chat is successfully selected.
type ChatSelectedPayload struct {
	SessionID    string `json:"session_id"`
	ChatName     string `json:"chat_name"`
	Description  string `json:"description"`
	Message      string `json:"message"`
	MessageCount int    `json:"message_count"`
}

// ChunkPayload represents a streaming content chunk.
type ChunkPayload struct {
	Content     string `json:"content"`
	First       bool   `json:"first"`
	Last        bool   `json:"last"`
	ContentType string `json:"content_type"` // "response" or "thinking"
}

// ToolCallPayload is sent when the model invokes a tool.
type ToolCallPayload struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Index     string `json:"index"`
	Streaming bool   `json:"streaming"`
}

// ThinkingPayload indicates whether the model is in a thinking/reasoning phase.
type ThinkingPayload struct {
	Status bool `json:"status"`
}

// CompletePayload signals completion of a response.
type CompletePayload struct {
	Message string `json:"message"`
}

// ErrorPayload carries an error message.
type ErrorPayload struct {
	Error string `json:"error"`
}

// ApprovalTargetPayload describes a single target requiring approval.
type ApprovalTargetPayload struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Details string `json:"details"`
}

// ApprovalRequestPayload is sent when tool execution requires user approval.
type ApprovalRequestPayload struct {
	ApprovalID string                   `json:"approval_id"`
	Targets    []ApprovalTargetPayload  `json:"targets"`
}

// MessageCountPayload carries the current message count.
type MessageCountPayload struct {
	Count int `json:"count"`
}

// StoppedPayload is sent when a response is stopped by the user.
type StoppedPayload struct {
	Message string `json:"message"`
}

// KeptPayload is sent after a keep hook execution.
type KeptPayload struct {
	ChatName string `json:"chat_name,omitempty"`
	Message  string `json:"message"`
}

// ClearedPayload is sent after the conversation context is cleared.
type ClearedPayload struct {
	ChatName     string `json:"chat_name,omitempty"`
	Message      string `json:"message"`
	MessageCount int    `json:"message_count"`
}

// FilePayload represents a file attachment in a chat request.
type FilePayload struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	FileSize int64  `json:"file_size,omitempty"`
}

// ChatRequest is the payload for select_chat and chat commands.
type ChatRequest struct {
	ChatName string        `json:"chat_name,omitempty"`
	Message  string        `json:"message,omitempty"`
	Files    []FilePayload `json:"files,omitempty"`
}

// ApprovalItem represents a single approval decision.
type ApprovalItem struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

// ApprovalResponsePayload is the payload for approval_response command.
type ApprovalResponsePayload struct {
	ApprovalID string                  `json:"approval_id"`
	Results    map[string]ApprovalItem `json:"results"`
}
