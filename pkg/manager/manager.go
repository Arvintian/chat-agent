package manager

import (
	"fmt"

	"github.com/cloudwego/eino/schema"
)

// Manager 管理对话上下文，提供智能的上下文管理功能
type Manager struct {
	// messages 存储对话历史消息
	messages []*schema.Message

	// maxMessages 限制上下文中的最大消息数
	maxMessages int

	// systemPrompt 系统提示信息
	systemPrompt string
}

// NewManager 创建一个新的Manager实例
func NewManager(systemPrompt string, maxMessages int) *Manager {
	return &Manager{
		messages:     make([]*schema.Message, 0),
		maxMessages:  maxMessages,
		systemPrompt: systemPrompt,
	}
}

// AddMessage 添加消息到上下文
func (m *Manager) AddMessage(message *schema.Message) {
	m.messages = append(m.messages, message)

	// 如果消息数量超过限制，移除最早的消息（保留系统消息）
	if len(m.messages) > m.maxMessages {
		m.trimMessages()
	}
}

// trimMessages 修剪消息历史，保留系统消息和最近的消息
func (m *Manager) trimMessages() {
	if len(m.messages) <= m.maxMessages {
		return
	}

	// 保留系统消息
	var newMessages []*schema.Message
	for _, msg := range m.messages {
		if msg.Role == schema.System {
			newMessages = append(newMessages, msg)
		}
	}

	// 保留最近的消息（不包括系统消息）
	recentMessages := m.messages[len(m.messages)-m.maxMessages+len(newMessages):]
	newMessages = append(newMessages, recentMessages...)

	m.messages = newMessages
}

// GetMessages 获取当前上下文中的所有消息
func (m *Manager) GetMessages() []*schema.Message {
	return m.messages
}

// Clear 清空上下文（保留系统消息）
func (m *Manager) Clear() {
	var systemMessages []*schema.Message
	for _, msg := range m.messages {
		if msg.Role == schema.System {
			systemMessages = append(systemMessages, msg)
		}
	}
	m.messages = systemMessages
}

// Init 初始化上下文，添加系统提示
func (m *Manager) Init() {
	m.Clear()
	if m.systemPrompt != "" {
		m.messages = append(m.messages, schema.SystemMessage(m.systemPrompt))
	}
}

// GetSummary 获取对话摘要
func (m *Manager) GetSummary() string {
	if len(m.messages) == 0 {
		return "空对话"
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

	return fmt.Sprintf("对话包含 %d 条用户消息和 %d 条助手回复", userMessages, assistantMessages)
}

// GetLastUserMessage 获取最后一条用户消息
func (m *Manager) GetLastUserMessage() *schema.Message {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == schema.User {
			return m.messages[i]
		}
	}
	return nil
}

// GetLastAssistantMessage 获取最后一条助手消息
func (m *Manager) GetLastAssistantMessage() *schema.Message {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == schema.Assistant {
			return m.messages[i]
		}
	}
	return nil
}

// SetMaxMessages 设置最大消息数量
func (m *Manager) SetMaxMessages(max int) {
	m.maxMessages = max
	m.trimMessages()
}

// GetContextSize 获取当前上下文大小（消息数量）
func (m *Manager) GetContextSize() int {
	return len(m.messages)
}
