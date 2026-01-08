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

// ChatBot struct for the chatbot
type ChatBot struct {
	runner *adk.Runner

	// agent for interacting with the large language model
	agent *adk.ChatModelAgent

	// ctx is the application context for controlling request lifecycle
	ctx context.Context

	// manager handles conversation context management
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

// StreamChat performs streaming chat conversation
func (cb *ChatBot) StreamChat(ctx context.Context, userInput string) error {
	// Add user message to context
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// Get context messages
	messages := cb.manager.GetMessages()

	// Generate streaming response
	streamReader := cb.runner.Run(ctx, messages)

	response, debug := strings.Builder{}, false
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
			if len(toolMap) > 0 && !strings.HasSuffix(response.String(), "\n") {
				fmt.Print("\n")
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
			}
			fmt.Print(event.Output.MessageOutput.Message.Content)
			response.WriteString(event.Output.MessageOutput.Message.Content)
		}
		if event.Output.MessageOutput.Role == schema.Tool {
			fmt.Print("\n---\n")
		}
	}

	message := response.String()
	if !strings.HasSuffix(message, "\n") {
		fmt.Print("\n")
	}
	cb.manager.AddMessage(schema.AssistantMessage(message, nil))

	return nil
}

// GetContextSummary retrieves context summary
func (cb *ChatBot) GetContextSummary() string {
	return cb.manager.GetSummary()
}

// ClearContext clears the context
func (cb *ChatBot) ClearContext() {
	cb.manager.Clear()
}

// GetContextSize gets the context size
func (cb *ChatBot) GetContextSize() int {
	return cb.manager.GetContextSize()
}

// SetMaxContextSize sets maximum context size
func (cb *ChatBot) SetMaxContextSize(maxMessages int) {
	cb.manager.SetMaxMessages(maxMessages)
}

// GetLastUserMessage gets the last user message
func (cb *ChatBot) GetLastUserMessage() *schema.Message {
	return cb.manager.GetLastUserMessage()
}

// GetLastAssistantMessage gets the last assistant message
func (cb *ChatBot) GetLastAssistantMessage() *schema.Message {
	return cb.manager.GetLastAssistantMessage()
}
