package chatbot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Arvintian/chat-agent/pkg/manager"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/store"
	"github.com/ollama/ollama/readline"

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

	scanner *readline.Instance
}

func NewChatBot(ctx context.Context, agent *adk.ChatModelAgent, manager *manager.Manager, scanner *readline.Instance) ChatBot {
	return ChatBot{
		ctx: ctx,
		runner: adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           agent,
			EnableStreaming: true,
			CheckPointStore: store.NewInMemoryStore(),
		}),
		agent:   agent,
		manager: manager,
		scanner: scanner,
	}
}

// StreamChat performs streaming chat conversation
func (cb *ChatBot) StreamChat(ctx context.Context, userInput string) error {
	// Add user message to context
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// Get context messages
	messages := cb.manager.GetMessages()

	// Generate streaming response
	streamReader := cb.runner.Run(ctx, messages, adk.WithCheckPointID("local"))

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

		if event.Action != nil && event.Action.Interrupted != nil {
			var err error
			targets := map[string]any{}
			for _, intCtx := range event.Action.Interrupted.InterruptContexts {
				approvalInfo, ok := intCtx.Info.(*mcp.ApprovalInfo)
				if !ok {
					continue
				}
				var apResult *mcp.ApprovalResult
				cb.scanner.Prompt.Placeholder = "Y/N"
				for {
					fmt.Printf("%s\n", approvalInfo.String())
					line, err := cb.scanner.Readline()
					switch {
					case errors.Is(err, io.EOF):
						return fmt.Errorf("wait approval error")
					case errors.Is(err, readline.ErrInterrupt):
						return fmt.Errorf("wait approval error")
					case err != nil:
						return err
					}
					input := strings.TrimSpace(line)
					if strings.ToUpper(input) == "Y" {
						apResult = &mcp.ApprovalResult{Approved: true}
						break
					} else if strings.ToUpper(input) == "N" {
						apResult = &mcp.ApprovalResult{Approved: false}
						break
					}
					fmt.Println("Invalid input, please input Y or N")
				}
				targets[intCtx.ID] = apResult
			}
			if len(targets) < 1 {
				return fmt.Errorf("wait approval error")
			}
			streamReader, err = cb.runner.ResumeWithParams(ctx, "local", &adk.ResumeParams{
				Targets: targets,
			})
			if err != nil {
				return err
			}
			continue
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
			toolMap, filter := map[int][]*schema.Message{}, NewStreamFilter()
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
					if out := filter.Process(message.ReasoningContent); out != nil {
						fmt.Print(*out)
					}
				}
				if message.Content != "" {
					if out := filter.Process(message.Content); out != nil {
						fmt.Print(*out)
					}
				}
				response.WriteString(message.Content)
			}
			if out := filter.Finish(); out != nil {
				fmt.Print(*out)
			}
			if len(toolMap) > 0 {
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

	fmt.Print("\n")
	cb.manager.AddMessage(schema.AssistantMessage(response.String(), nil))

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
