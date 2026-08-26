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
	"github.com/Arvintian/readline"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/hekmon/liveterm/v2"
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

	// handler for output (CLI or WebSocket)
	handler Handler
}

// compressionHint is shown to the user right before the (blocking) history
// compression summary call pauses the request.
const compressionHint = "[Context is being compressed, this may take a while...]"

func NewChatBot(ctx context.Context, agent *adk.ChatModelAgent, manager *manager.Manager, scanner *readline.Instance, persistence *store.PersistenceStore) ChatBot {
	var checkPointStore compose.CheckPointStore
	if persistence != nil {
		checkPointStore = store.NewHybridCheckPointStore(persistence)
	} else {
		checkPointStore = store.NewInMemoryStore()
	}

	cb := ChatBot{
		ctx: ctx,
		runner: adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           agent,
			EnableStreaming: true,
			CheckPointStore: checkPointStore,
		}),
		agent:   agent,
		manager: manager,
		scanner: scanner,
	}

	// CLI mode (readline scanner present): print the hint on stdout before the
	// blocking compression summary call.
	if scanner != nil {
		manager.SetCompressionProgressCallback(func(ctx context.Context) {
			fmt.Println(compressionHint)
		})
	}

	return cb
}

// StreamChat performs streaming chat conversation with CLI output
func (cb *ChatBot) StreamChat(ctx context.Context, userInput string) error {
	// Start the new round first: this is where (blocking) history compression
	// runs, so the context snapshot taken afterwards already reflects the
	// compressed history and this request uses it directly.
	cb.manager.IncRound(ctx)

	// Get context messages
	messages := cb.manager.GetMessages()

	userMessage := schema.UserMessage(userInput)

	// Add user message to context
	cb.manager.AddMessage(ctx, userMessage)

	messages = append(messages, userMessage)

	// Generate streaming response
	streamReader := cb.runner.Run(ctx, messages, adk.WithCheckPointID("local"))

	response, reasoningContent, debug := strings.Builder{}, strings.Builder{}, false
	// raw* builders accumulate the unfiltered stream (no trimming, no
	// decoding) so the messages persisted to the manager stay byte-identical
	// to what the model produced and saw, keeping prompt-cache prefixes stable
	// across rounds.
	rawResponse, rawReasoning := strings.Builder{}, strings.Builder{}
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
			cb.manager.AddMessage(ctx, event.Output.MessageOutput.Message)
			fmt.Printf("ToolCall: (%s) Completed", event.Output.MessageOutput.ToolName)
			if !debug {
				fmt.Print("\n---\n")
				continue
			} else {
				fmt.Println()
			}
		}

		response.Reset()
		reasoningContent.Reset()
		rawResponse.Reset()
		rawReasoning.Reset()
		toolMap := map[int][]*schema.Message{}
		if event.Output.MessageOutput.MessageStream != nil {
			reasoning, firstword := false, false
			// Use separate filters for thinking and response to avoid output interleaving
			thinkingFilter := NewStreamFilter()
			responseFilter := NewStreamFilter()
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
									Index: index,
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
					reasoning = true
				}
				if message.ReasoningContent != "" {
					rawReasoning.WriteString(message.ReasoningContent)
					//Decode JSON-encoded ReasoningContent (e.g. from OpenRouter)
					decodedReasoning := message.ReasoningContent
					if err := json.Unmarshal([]byte(message.ReasoningContent), &decodedReasoning); err != nil {
						decodedReasoning = message.ReasoningContent
					}
					// Skip whitespace-only chunks at the beginning (before any meaningful content)
					if reasoningContent.Len() > 0 || strings.TrimSpace(decodedReasoning) != "" {
						if reasoning && reasoningContent.Len() == 0 {
							// Strip leading whitespace from the first meaningful thinking chunk
							decodedReasoning = TrimLeadingWhitespace(decodedReasoning)
							if decodedReasoning != "" {
								fmt.Print("Thinking:\n")
							}
						}
						if out := thinkingFilter.Process(decodedReasoning); out != nil {
							fmt.Print(*out)
						}
						reasoningContent.WriteString(decodedReasoning)
					}
				}
				if message.Content != "" && reasoning && !firstword {
					// Transition from thinking to response: flush thinking filter first, then separator
					if reasoningContent.Len() > 0 {
						if out := thinkingFilter.Finish(); out != nil {
							fmt.Print(*out)
						}
						fmt.Print("\n---\n")
					}
					firstword = true
				}
				if message.Content != "" {
					rawResponse.WriteString(message.Content)
					// Skip whitespace-only chunks at the beginning (before any meaningful content)
					if response.Len() > 0 || strings.TrimSpace(message.Content) != "" {
						content := message.Content
						// Strip leading whitespace from the first meaningful response chunk
						if response.Len() == 0 {
							content = TrimLeadingWhitespace(content)
						}
						if out := responseFilter.Process(content); out != nil {
							fmt.Print(*out)
						}
						response.WriteString(content)
					}
				}
			}
			// Flush remaining buffers at end
			if out := thinkingFilter.Finish(); out != nil {
				fmt.Print(*out)
			}
			if out := responseFilter.Finish(); out != nil {
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
				for i, tc := range event.Output.MessageOutput.Message.ToolCalls {
					index := tc.Index
					if index == nil {
						index = &i
					}
					toolMap[*index] = append(toolMap[*index], &schema.Message{
						Role: event.Output.MessageOutput.Message.Role,
						ToolCalls: []schema.ToolCall{{
							ID:    tc.ID,
							Type:  tc.Type,
							Index: index,
							Function: schema.FunctionCall{
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							},
						}},
					})
					line, _ := TruncateToTermWidth(fmt.Sprintf("ToolCall: (%s) %s", tc.Function.Name, tc.Function.Arguments))
					fmt.Print(line)
					fmt.Print("\n---\n")
				}
			}
			fmt.Print(event.Output.MessageOutput.Message.Content)
			response.WriteString(event.Output.MessageOutput.Message.Content)
			reasoningContent.WriteString(event.Output.MessageOutput.Message.ReasoningContent)
			rawResponse.WriteString(event.Output.MessageOutput.Message.Content)
			rawReasoning.WriteString(event.Output.MessageOutput.Message.ReasoningContent)
		}
		if event.Output.MessageOutput.Role == schema.Tool {
			fmt.Print("\n---\n")
		}
		if len(toolMap) > 0 {
			toolMsg := schema.Message{
				Role:      schema.Assistant,
				ToolCalls: make([]schema.ToolCall, len(toolMap)),
				// Persist the raw (unfiltered) content: it must stay
				// byte-identical to what the model produced, so the next
				// round's prompt prefix matches the previous round's tail
				// for prompt caches.
				Content:          rawResponse.String(),
				ReasoningContent: rawReasoning.String(),
			}
			for index, msgs := range toolMap {
				m, err := schema.ConcatMessages(msgs)
				if err != nil {
					continue
				}
				toolMsg.ToolCalls[index] = m.ToolCalls[0]
			}
			cb.manager.AddMessage(ctx, &toolMsg)
		}
	}

	fmt.Print("\n")
	cb.manager.AddMessage(ctx, &schema.Message{
		Role: schema.Assistant,
		// Raw (unfiltered) content, byte-identical to the model output, to
		// keep prompt-cache prefixes stable across rounds.
		Content:          rawResponse.String(),
		ReasoningContent: rawReasoning.String(),
	})

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
