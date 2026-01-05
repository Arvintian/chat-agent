package chatbot

import (
	"chat-agent/pkg/manager"
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ChatBot 聊天机器人结构体
type ChatBot struct {
	// model 用于与大语言模型进行交互
	model model.ToolCallingChatModel

	// ctx 是应用的上下文，用于控制请求生命周期
	ctx context.Context

	// manager 负责管理对话上下文
	manager *manager.Manager
}

func NewChatBot(ctx context.Context, model model.ToolCallingChatModel, manager *manager.Manager) ChatBot {
	return ChatBot{
		ctx:     ctx,
		model:   model,
		manager: manager,
	}
}

// Chat 进行普通聊天对话
func (cb *ChatBot) Chat(userInput string) (string, error) {
	// 添加用户消息到上下文
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// 获取上下文消息
	messages := cb.manager.GetMessages()

	// 生成回复
	result, err := cb.model.Generate(cb.ctx, messages)
	if err != nil {
		return "", err
	}

	// 添加助手回复到上下文
	cb.manager.AddMessage(schema.AssistantMessage(result.Content, nil))

	return result.Content, nil
}

// StreamChat 进行流式聊天对话
func (cb *ChatBot) StreamChat(userInput string) error {
	// 添加用户消息到上下文
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// 获取上下文消息
	messages := cb.manager.GetMessages()

	// 生成流式回复
	streamReader, err := cb.model.Stream(cb.ctx, messages)
	if err != nil {
		return err
	}
	defer streamReader.Close()

	var response strings.Builder
	reasoning, firstword := false, false
	for {
		message, err := streamReader.Recv()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return err
		}
		if message.ReasoningContent != "" && !reasoning {
			fmt.Print("Thinking:\n")
			reasoning = true
		}
		if message.Content != "" && reasoning && !firstword {
			fmt.Print("\n---\n")
			firstword = true
		}
		if message.ReasoningContent != "" {
			fmt.Print(message.ReasoningContent)
		}
		if message.Content != "" {
			fmt.Print(message.Content)
		}
		response.WriteString(message.Content)
	}
	fmt.Print("\n\n")

	// 添加完整的助手回复到上下文
	cb.manager.AddMessage(schema.AssistantMessage(response.String(), nil))

	return nil
}

// GetContextSummary 获取上下文摘要
func (cb *ChatBot) GetContextSummary() string {
	return cb.manager.GetSummary()
}

// ClearContext 清空上下文
func (cb *ChatBot) ClearContext() {
	cb.manager.Clear()
}

// GetContextSize 获取上下文大小
func (cb *ChatBot) GetContextSize() int {
	return cb.manager.GetContextSize()
}

// SetMaxContextSize 设置最大上下文大小
func (cb *ChatBot) SetMaxContextSize(maxMessages int) {
	cb.manager.SetMaxMessages(maxMessages)
}

// GetLastUserMessage 获取最后一条用户消息
func (cb *ChatBot) GetLastUserMessage() *schema.Message {
	return cb.manager.GetLastUserMessage()
}

// GetLastAssistantMessage 获取最后一条助手消息
func (cb *ChatBot) GetLastAssistantMessage() *schema.Message {
	return cb.manager.GetLastAssistantMessage()
}
