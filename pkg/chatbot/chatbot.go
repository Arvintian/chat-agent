package chatbot

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Arvintian/chat-agent/pkg/manager"

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

	response, willToolCall, debug := strings.Builder{}, false, false
	if v, ok := cb.ctx.Value("debug").(bool); ok {
		debug = v
	}
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
			fmt.Printf("ToolCall: (%s) Completed", event.Output.MessageOutput.ToolName)
			if !debug {
				fmt.Print("\n---\n")
				continue
			} else {
				fmt.Println()
			}
		}

		response.Reset()
		if event.Output.MessageOutput.MessageStream != nil {
			reasoning, firstword := false, false
			toolMap := map[int][]*schema.Message{}
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error receiving message stream: %w", err)
				}
				if len(message.ToolCalls) > 0 {
					for i, tc := range message.ToolCalls {
						index := tc.Index
						if index == nil {
							//Assuming the order of tool calls is sequential
							index = &i
						}
						toolMap[*index] = append(toolMap[*index], &schema.Message{
							Role: message.Role,
							ToolCalls: []schema.ToolCall{
								{
									ID:    tc.ID,
									Type:  tc.Type,
									Index: tc.Index,
									Function: schema.FunctionCall{
										Name:      tc.Function.Name,
										Arguments: tc.Function.Arguments,
									},
								},
							},
						})
					}
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
			if len(toolMap) > 0 {
				fmt.Println()
				willToolCall = true
			}
			for _, msgs := range toolMap {
				m, err := schema.ConcatMessages(msgs)
				if err != nil {
					return fmt.Errorf("ConcatMessage failed: %v", err)
				}
				fmt.Printf("ToolCall: (%s) %s", m.ToolCalls[0].Function.Name, m.ToolCalls[0].Function.Arguments)
				fmt.Print("\n---\n")
			}
		} else if event.Output.MessageOutput.Message != nil {
			if len(event.Output.MessageOutput.Message.ToolCalls) > 0 {
				for _, tc := range event.Output.MessageOutput.Message.ToolCalls {
					fmt.Printf("ToolCall: (%s) %s", tc.Function.Name, tc.Function.Arguments)
					fmt.Print("\n---\n")
				}
				willToolCall = true
			}
			fmt.Print(event.Output.MessageOutput.Message.Content)
			response.WriteString(event.Output.MessageOutput.Message.Content)
		}
		if event.Output.MessageOutput.Role == schema.Tool {
			fmt.Print("\n---\n")
		} else {
			if !willToolCall {
				fmt.Print("\n\n")
			}
		}
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
