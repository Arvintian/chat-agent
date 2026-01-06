package chatbot

import (
	"chat-agent/pkg/manager"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ChatBot 聊天机器人结构体
type ChatBot struct {
	runner *adk.Runner

	// agent 用于与大语言模型进行交互
	agent *adk.ChatModelAgent

	// ctx 是应用的上下文，用于控制请求生命周期
	ctx context.Context

	// manager 负责管理对话上下文
	manager *manager.Manager
}

func NewChatBot(ctx context.Context, agent *adk.ChatModelAgent, manager *manager.Manager) ChatBot {
	return ChatBot{
		ctx: ctx,
		runner: adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           agent,
			EnableStreaming: true,
		}),
		agent:   agent,
		manager: manager,
	}
}

// StreamChat 进行流式聊天对话
func (cb *ChatBot) StreamChat(userInput string) error {
	// 添加用户消息到上下文
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// 获取上下文消息
	messages := cb.manager.GetMessages()

	// 生成流式回复
	streamReader := cb.runner.Run(cb.ctx, messages)

	var response strings.Builder
	for {
		event, ok := streamReader.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return event.Err
		}
		response.Reset()
		if event.Output.MessageOutput.IsStreaming {
			reasoning, firstword := false, false
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error receiving message stream: %w", err)
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
		} else {
			fmt.Print(event.Output.MessageOutput.Message.Content)
			response.WriteString(event.Output.MessageOutput.Message.Content)
		}
		fmt.Print("\n\n")
	}

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
