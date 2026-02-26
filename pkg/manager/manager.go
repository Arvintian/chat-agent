package manager

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	DefaultMaxMessages int = 10
)

// Manager manages conversation context with intelligent context management capabilities
type Manager struct {
	// messages stores the conversation history
	messages [][]*schema.Message

	// maxMessages limits the maximum number of messages in the context
	maxMessages int

	round int

	// chatmodel for compressing messages when threshold is reached
	chatmodel model.ToolCallingChatModel

	mu sync.Mutex

	// compression related fields
	compressing    bool                // indicates if compression is in progress
	compressBuffer [][]*schema.Message // buffer for original messages waiting to be compressed
}

// NewManager creates a new Manager instance
func NewManager(maxMessages int) *Manager {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}
	return &Manager{
		messages:       make([][]*schema.Message, 0),
		maxMessages:    maxMessages,
		round:          0,
		chatmodel:      nil,
		compressing:    false,
		compressBuffer: make([][]*schema.Message, 0),
	}
}

// SetChatModel sets the chat model for message compression
func (m *Manager) SetChatModel(chatmodel model.ToolCallingChatModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatmodel = chatmodel
}

// AddMessage adds a message to the context
func (m *Manager) AddMessage(ctx context.Context, message *schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure we have at least one round
	if len(m.messages) == 0 {
		m.messages = append(m.messages, make([]*schema.Message, 0))
		m.round = 0
	}

	m.messages[m.round] = append(m.messages[m.round], message)

	// If the number of rounds exceeds the limit, trim messages
	m.trimMessages(ctx)
}

func (m *Manager) IncRound() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate and clean up mismatched tool messages and toolcalls in current round
	if len(m.messages) > 0 {
		currentRound := m.messages[m.round]
		validMessages := m.validateAndCleanRound(currentRound)
		m.messages[m.round] = validMessages
	}

	m.messages = append(m.messages, make([]*schema.Message, 0))
	m.round = len(m.messages) - 1
}

// validateAndCleanRound validates that tool messages and toolcalls are paired correctly
// Returns cleaned message slice with mismatched messages removed
func (m *Manager) validateAndCleanRound(messages []*schema.Message) []*schema.Message {
	// Collect all toolcall IDs from assistant messages
	toolcallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolcallIDs[tc.ID] = true
				}
			}
		}
	}

	// Collect all tool response messages with their ToolCallID
	toolResponses := make(map[string]*schema.Message)
	for _, msg := range messages {
		if msg.Role == schema.Tool && msg.ToolCallID != "" {
			toolResponses[msg.ToolCallID] = msg
		}
	}

	// Identify mismatched toolcalls (no corresponding tool response)
	unmatchedToolcalls := make(map[string]bool)
	for id := range toolcallIDs {
		if _, exists := toolResponses[id]; !exists {
			unmatchedToolcalls[id] = true
		}
	}

	// Identify mismatched tool responses (no corresponding toolcall)
	unmatchedToolResponses := make(map[string]bool)
	for id := range toolResponses {
		if _, exists := toolcallIDs[id]; !exists {
			unmatchedToolResponses[id] = true
		}
	}

	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			hasUnmatched := false
			for _, tc := range msg.ToolCalls {
				if unmatchedToolcalls[tc.ID] {
					hasUnmatched = true
					break
				}
			}
			if hasUnmatched {
				for _, tc := range msg.ToolCalls {
					unmatchedToolResponses[tc.ID] = true
				}
			}
		}
	}

	// If no mismatches, return original messages
	if len(unmatchedToolcalls) == 0 && len(unmatchedToolResponses) == 0 {
		return messages
	}

	// Filter out mismatched messages
	validMessages := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		keep := true

		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			// Check if any toolcall in this message is unmatched
			hasUnmatched := false
			for _, tc := range msg.ToolCalls {
				if unmatchedToolcalls[tc.ID] {
					hasUnmatched = true
					break
				}
			}
			if hasUnmatched {
				// Remove this assistant message as it contains unmatched toolcalls
				keep = false
			}
		} else if msg.Role == schema.Tool && msg.ToolCallID != "" {
			// Check if this tool response is unmatched
			if unmatchedToolResponses[msg.ToolCallID] {
				keep = false
			}
		}

		if keep {
			validMessages = append(validMessages, msg)
		}
	}

	return validMessages
}

// trimMessages trims the message history, preserving system messages and recent messages
// When messages exceed threshold, compresses half of the window using chatmodel
func (m *Manager) trimMessages(ctx context.Context) {
	// Start async compression early at ~70% of maxMessages threshold
	// This gives time for compression to complete before hitting the hard limit
	asyncCompressThreshold := int(float64(m.maxMessages) * 0.7)
	if len(m.messages) >= asyncCompressThreshold && !m.compressing && m.chatmodel != nil {
		go m.compressMessagesAsync(ctx)
	}
}

// compressMessagesAsync performs asynchronous compression in a goroutine
func (m *Manager) compressMessagesAsync(ctx context.Context) {
	m.mu.Lock()
	if m.compressing {
		m.mu.Unlock()
		return
	}
	m.compressing = true

	// Calculate how many rounds to compress (half of the current window, excluding the most recent)
	numToCompress := len(m.messages) / 2
	if numToCompress < 1 {
		numToCompress = 1
	}

	// Copy messages to compress buffer (original messages waiting to be compressed)
	messagesToCompress := make([][]*schema.Message, 0)
	for i := 0; i < numToCompress && i < len(m.messages)-1; i++ {
		roundCopy := make([]*schema.Message, len(m.messages[i]))
		copy(roundCopy, m.messages[i])
		messagesToCompress = append(messagesToCompress, roundCopy)
	}

	// If prev compression not success
	m.compressBuffer = append(m.compressBuffer, messagesToCompress...)
	if len(m.compressBuffer) > m.maxMessages {
		m.compressBuffer = m.compressBuffer[len(m.compressBuffer)-m.maxMessages:]
	}

	m.messages = m.messages[numToCompress:]
	m.round = len(m.messages) - 1
	m.mu.Unlock()

	// Flatten messages for compression
	flatMessages := make([]*schema.Message, 0)
	for _, round := range messagesToCompress {
		flatMessages = append(flatMessages, round...)
	}

	// Perform compression without holding the main lock
	summary := ""
	if len(flatMessages) > 0 {
		summary = m.doCompression(ctx, [][]*schema.Message{flatMessages})
	}

	// Mark compression as complete
	m.mu.Lock()
	m.compressing = false
	if summary != "" {
		summaryMessage := schema.AssistantMessage(fmt.Sprintf("[Conversation Summary]: %s", summary), nil)
		if len(m.messages) > 0 && len(m.messages[0]) > 0 && strings.HasPrefix(m.messages[0][0].Content, "[Conversation Summary]:") {
			m.messages = m.messages[1:]
		}
		m.messages = append([][]*schema.Message{{summaryMessage}}, m.messages...)
		m.round = len(m.messages) - 1
		m.compressBuffer = make([][]*schema.Message, 0)
	}
	m.mu.Unlock()
}

// doCompression performs the actual compression logic
func (m *Manager) doCompression(ctx context.Context, messages [][]*schema.Message) string {
	// Flatten messages
	flatMessages := make([]*schema.Message, 0)
	for _, round := range messages {
		flatMessages = append(flatMessages, round...)
	}

	if len(flatMessages) == 0 {
		return ""
	}

	// Generate summary using chatmodel with inherited context
	summaryMsgs := []*schema.Message{
		schema.SystemMessage("You are a conversation summarizer. Summarize the following conversation concisely while preserving key information, decisions, and context. Output only the summary."),
	}
	summaryMsgs = append(summaryMsgs, flatMessages...)

	stream, err := m.chatmodel.Generate(ctx, summaryMsgs)
	if err != nil {
		logger.GetDefaultLogger().Errorf("Context Manager %v", err)
		return ""
	}

	summaryContent := strings.TrimSpace(stream.Content)
	if summaryContent == "" {
		return "Previous conversation summarized."
	}

	return summaryContent
}

// GetMessages retrieves all messages in the current context
func (m *Manager) GetMessages() []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Append normal messages
	recentMessages := make([]*schema.Message, 0)
	for _, round := range m.compressBuffer {
		recentMessages = append(recentMessages, round...)
	}
	for _, round := range m.messages {
		recentMessages = append(recentMessages, round...)
	}

	return recentMessages
}

// Clear clears the context (preserves system messages)
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.round = 0
	m.messages = make([][]*schema.Message, 0)
}

// GetSummary generates a summary of the conversation
func (m *Manager) GetSummary() string {
	if len(m.messages) == 0 {
		return "Empty conversation"
	}

	var userMessages, assistantMessages, toolMessages int
	for _, round := range m.messages {
		for _, msg := range round {
			switch msg.Role {
			case schema.User:
				userMessages++
			case schema.Assistant:
				assistantMessages++
			case schema.Tool:
				toolMessages++
			}
		}
	}

	return fmt.Sprintf("Conversation contains %d user messages, %d assistant, %d tool replies", userMessages, assistantMessages, toolMessages)
}
