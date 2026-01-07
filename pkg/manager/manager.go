package manager

import (
	"fmt"

	"github.com/cloudwego/eino/schema"
)

const (
	DefaultMaxMessages int = 20
)

// Manager manages conversation context with intelligent context management capabilities
type Manager struct {
	// messages stores the conversation history
	messages []*schema.Message

	// maxMessages limits the maximum number of messages in the context
	maxMessages int
}

// NewManager creates a new Manager instance
func NewManager(maxMessages int) *Manager {
	if maxMessages <= 0 {
		maxMessages = DefaultMaxMessages
	}
	return &Manager{
		messages:    make([]*schema.Message, 0),
		maxMessages: maxMessages,
	}
}

// AddMessage adds a message to the context
func (m *Manager) AddMessage(message *schema.Message) {
	m.messages = append(m.messages, message)

	// If the number of messages exceeds the limit, remove the oldest ones (preserve system messages)
	if len(m.messages) > m.maxMessages {
		m.trimMessages()
	}
}

// trimMessages trims the message history, preserving system messages and recent messages
func (m *Manager) trimMessages() {
	if len(m.messages) <= m.maxMessages {
		return
	}

	// Preserve system messages
	var newMessages []*schema.Message
	for _, msg := range m.messages {
		if msg.Role == schema.System {
			newMessages = append(newMessages, msg)
		}
	}

	// Keep recent messages (excluding system messages)
	recentMessages := m.messages[len(m.messages)-m.maxMessages+len(newMessages):]
	newMessages = append(newMessages, recentMessages...)

	m.messages = newMessages
}

// GetMessages retrieves all messages in the current context
func (m *Manager) GetMessages() []*schema.Message {
	return m.messages
}

// Clear clears the context (preserves system messages)
func (m *Manager) Clear() {
	var systemMessages []*schema.Message
	for _, msg := range m.messages {
		if msg.Role == schema.System {
			systemMessages = append(systemMessages, msg)
		}
	}
	m.messages = systemMessages
}

// Init initializes the context by adding system prompts
func (m *Manager) Init() {
	m.Clear()
}

// GetSummary generates a summary of the conversation
func (m *Manager) GetSummary() string {
	if len(m.messages) == 0 {
		return "Empty conversation"
	}

	var userMessages, assistantMessages int
	for _, msg := range m.messages {
		switch msg.Role {
		case schema.User:
			userMessages++
		case schema.Assistant:
			assistantMessages++
		}
	}

	return fmt.Sprintf("Conversation contains %d user messages and %d assistant replies", userMessages, assistantMessages)
}

// GetLastUserMessage retrieves the last user message
func (m *Manager) GetLastUserMessage() *schema.Message {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == schema.User {
			return m.messages[i]
		}
	}
	return nil
}

// GetLastAssistantMessage retrieves the last assistant message
func (m *Manager) GetLastAssistantMessage() *schema.Message {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == schema.Assistant {
			return m.messages[i]
		}
	}
	return nil
}

// SetMaxMessages sets the maximum number of messages
func (m *Manager) SetMaxMessages(max int) {
	m.maxMessages = max
	m.trimMessages()
}

// GetContextSize returns the current context size (number of messages)
func (m *Manager) GetContextSize() int {
	return len(m.messages)
}
