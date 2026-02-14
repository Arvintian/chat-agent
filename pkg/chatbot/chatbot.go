package chatbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Arvintian/chat-agent/pkg/manager"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/store"
	"github.com/ollama/ollama/readline"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/hekmon/liveterm/v2"
)

// Handler interface for handling chat output events
// This allows the same streaming logic to be used in different contexts
// (CLI with readline, WebSocket, etc.)
type Handler interface {
	// SendChunk sends a content chunk with position markers
	SendChunk(content string, first, last bool)

	// SendToolCall sends a tool call notification
	SendToolCall(name string)

	// SendThinking sends a thinking indicator
	SendThinking(status bool)

	// SendComplete sends a completion signal
	SendComplete(message string)

	// SendError sends an error message
	SendError(err string)
}

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

	// handler for output (CLI or WebSocket)
	handler Handler
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

// SetHandler sets the output handler for the chatbot
func (cb *ChatBot) SetHandler(handler Handler) {
	cb.handler = handler
}

// StreamChat performs streaming chat conversation with CLI output
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
				cb.scanner.HistoryDisable()
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
					cb.scanner.History.Buf.Remove(cb.scanner.History.Size() - 1)
					cb.scanner.History.Pos = cb.scanner.History.Size()
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
			finalToolMap, toolStart, toolOutput, toolMu := map[int][]*schema.Message{}, false, strings.Builder{}, sync.Mutex{}
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error receiving message stream: %w", err)
				}
				if len(message.ToolCalls) > 0 {
					if !toolStart {
						fmt.Print("\n")
						liveterm.RefreshInterval = 200 * time.Millisecond
						liveterm.Output = os.Stdout
						liveterm.SetSingleLineUpdateFx(func() string {
							toolMu.Lock()
							defer toolMu.Unlock()
							return strings.TrimRight(toolOutput.String(), "\n")
						})
						if err := liveterm.Start(); err != nil {
							return err
						}
						defer func() {
							if toolStart {
								liveterm.Stop(false)
							}
						}()
						toolStart = true
					}
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
					toolMu.Lock()
					toolOutput.Reset()
					for k, msgs := range toolMap {
						m, err := schema.ConcatMessages(msgs)
						if err != nil {
							toolMu.Unlock()
							return fmt.Errorf("ConcatMessage failed: %v", err)
						}
						line, truncate := TruncateToTermWidth(fmt.Sprintf("ToolCall: (%s) %s", m.ToolCalls[0].Function.Name, m.ToolCalls[0].Function.Arguments))
						if truncate {
							finalToolMap[k] = msgs
						}
						toolOutput.WriteString(line)
						toolOutput.WriteString("\n---\n")
					}
					toolMu.Unlock()
				}
				if message.ReasoningContent != "" && !reasoning {
					fmt.Print("Thinking:\n")
					reasoning = true
				}
				if message.ReasoningContent != "" {
					//Decode JSON-encoded ReasoningContent (e.g. from OpenRouter)
					decodedReasoning := message.ReasoningContent
					if err := json.Unmarshal([]byte(message.ReasoningContent), &decodedReasoning); err != nil {
						decodedReasoning = message.ReasoningContent
					}
					if out := filter.Process(decodedReasoning); out != nil {
						fmt.Print(*out)
					}
				}
				if message.Content != "" && reasoning && !firstword {
					fmt.Print("\n---\n")
					firstword = true
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
			if toolStart {
				toolStart = false
				liveterm.Stop(false)
			}
			if debug {
				for _, msgs := range finalToolMap {
					m, err := schema.ConcatMessages(msgs)
					if err != nil {
						return fmt.Errorf("ConcatMessage failed: %v", err)
					}
					fmt.Printf("ToolCall: (%s) %s", m.ToolCalls[0].Function.Name, m.ToolCalls[0].Function.Arguments)
					fmt.Print("\n---\n")
				}
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

// StreamChatWithHandler performs streaming chat with a custom handler
func (cb *ChatBot) StreamChatWithHandler(ctx context.Context, userInput string) error {
	if cb.handler == nil {
		return fmt.Errorf("handler not set")
	}

	// Add user message to context
	cb.manager.AddMessage(schema.UserMessage(userInput))

	// Get context messages
	messages := cb.manager.GetMessages()

	// Generate streaming response
	streamReader := cb.runner.Run(ctx, messages, adk.WithCheckPointID("web"))

	response := strings.Builder{}
	firstChunk := true

	for {
		event, ok := streamReader.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return event.Err
		}

		if event.Action != nil && event.Action.Interrupted != nil {
			// Handle interruption (approval requests)
			targets := map[string]any{}
			for _, intCtx := range event.Action.Interrupted.InterruptContexts {
				approvalInfo, ok := intCtx.Info.(*mcp.ApprovalInfo)
				if !ok {
					continue
				}
				cb.handler.SendThinking(false)
				var apResult *mcp.ApprovalResult
				for {
					fmt.Printf("%s\n", approvalInfo.String())
					fmt.Print("Y/N: ")
					var input string
					fmt.Scanln(&input)
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
			var err error
			streamReader, err = cb.runner.ResumeWithParams(ctx, "web", &adk.ResumeParams{
				Targets: targets,
			})
			if err != nil {
				return err
			}
			cb.handler.SendThinking(true)
			continue
		}

		if event.Output == nil {
			continue
		}

		if event.Output.MessageOutput.Role == schema.Tool {
			cb.handler.SendToolCall(event.Output.MessageOutput.ToolName)
			continue
		}

		response.Reset()
		if event.Output.MessageOutput.MessageStream != nil {
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error receiving message stream: %w", err)
				}

				if len(message.ToolCalls) > 0 {
					for _, tc := range message.ToolCalls {
						cb.handler.SendToolCall(tc.Function.Name)
					}
				}

				if message.Content != "" {
					cb.handler.SendChunk(message.Content, firstChunk, false)
					firstChunk = false
					response.WriteString(message.Content)
				}
			}
			// Send final chunk marker
			cb.handler.SendChunk("", false, true)
		} else if event.Output.MessageOutput.Message != nil {
			if len(event.Output.MessageOutput.Message.ToolCalls) > 0 {
				for _, tc := range event.Output.MessageOutput.Message.ToolCalls {
					cb.handler.SendToolCall(tc.Function.Name)
				}
			}
			if event.Output.MessageOutput.Message.Content != "" {
				cb.handler.SendChunk(event.Output.MessageOutput.Message.Content, firstChunk, false)
				firstChunk = false
				response.WriteString(event.Output.MessageOutput.Message.Content)
			}
			// Send final chunk marker
			cb.handler.SendChunk("", false, true)
		}
	}

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