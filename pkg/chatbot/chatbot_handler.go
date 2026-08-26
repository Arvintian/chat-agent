package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ApprovalTarget represents a single approval request target
type ApprovalTarget struct {
	ID            string
	ToolName      string
	ArgumentsInfo string
}

// ApprovalResultMap holds approval results for multiple targets
type ApprovalResultMap map[string]*mcp.ApprovalResult

// Handler interface for handling chat output events
// This allows the same streaming logic to be used in different contexts
// (CLI with readline, WebSocket, etc.)
type Handler interface {
	// SendChunk sends a content chunk with position markers
	// contentType: "response" or "thinking"
	SendChunk(content string, first, last bool, contentType string)

	// SendToolCall sends a tool call notification with name, arguments, index and streaming status
	// index: the tool call index
	// streaming: true if this is a streaming update (arguments may be partial), false when complete
	SendToolCall(name string, arguments string, id string, streaming bool)

	// SendThinking sends a thinking indicator
	SendThinking(status bool)

	// SendComplete sends a completion signal
	SendComplete(message string)

	// SendError sends an error message
	SendError(err string)

	// SendApprovalRequest sends an approval request to the client and waits for the result
	// targets: list of approval targets requiring user authorization
	// Returns a map of target IDs to their approval results
	SendApprovalRequest(targets []ApprovalTarget) (ApprovalResultMap, error)

	// SendMessageCount sends the current message count to the client
	SendMessageCount()
}

// SetHandler sets the output handler for the chatbot
func (cb *ChatBot) SetHandler(handler Handler) {
	cb.handler = handler
	// WebSocket/handler mode: push the hint to the client before the blocking
	// compression summary call (overrides the CLI stdout hint if both are set).
	cb.manager.SetCompressionProgressCallback(func(ctx context.Context) {
		handler.SendChunk(compressionHint, true, true, "system")
	})
}

// StreamChatWithHandler performs streaming chat with a custom handler
func (cb *ChatBot) StreamChatWithHandler(ctx context.Context, userInput string, files []FileData) error {
	if cb.handler == nil {
		return fmt.Errorf("handler not set")
	}

	// Start the new round first: this is where (blocking) history compression
	// runs, so the context snapshot taken afterwards already reflects the
	// compressed history and this request uses it directly.
	cb.manager.IncRound(ctx)

	// Get context messages
	messages := cb.manager.GetMessages()

	var userMessage *schema.Message

	// Add user message to context (with files if present)
	if len(files) > 0 {
		// Create multimodal message with text and files
		userMessage = createMultimodalUserMessage(userInput, files)
	} else {
		userMessage = schema.UserMessage(userInput)
	}

	cb.manager.AddMessage(ctx, userMessage)

	// Send message count update after adding user message
	cb.handler.SendMessageCount()

	messages = append(messages, userMessage)

	// Generate streaming response
	streamReader := cb.runner.Run(ctx, messages, adk.WithCheckPointID("web"))

	response := strings.Builder{}
	reasoningContent := strings.Builder{}
	// raw* builders accumulate the unfiltered stream (no trimming, no
	// decoding) so the messages persisted to the manager stay byte-identical
	// to what the model produced and saw, keeping prompt-cache prefixes stable
	// across rounds.
	rawResponse, rawReasoning := strings.Builder{}, strings.Builder{}
	firstChunk := true

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			cb.handler.SendComplete("")
			return ctx.Err()
		default:
		}

		event, ok := streamReader.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			cb.handler.SendError(event.Err.Error())
			return event.Err
		}

		if event.Action != nil && event.Action.Interrupted != nil {
			// Handle interruption (approval requests) via handler
			cb.handler.SendThinking(false)

			// Collect all approval targets from interrupt contexts
			approvalTargets := make([]ApprovalTarget, 0, len(event.Action.Interrupted.InterruptContexts))
			for _, intCtx := range event.Action.Interrupted.InterruptContexts {
				approvalInfo, ok := intCtx.Info.(*mcp.ApprovalInfo)
				if !ok {
					continue
				}
				approvalTargets = append(approvalTargets, ApprovalTarget{
					ID:            intCtx.ID,
					ToolName:      approvalInfo.ToolName,
					ArgumentsInfo: approvalInfo.ArgumentsInJSON,
				})
			}

			if len(approvalTargets) < 1 {
				err := fmt.Errorf("wait approval error")
				cb.handler.SendError(err.Error())
				return err
			}

			// Send approval request to handler and wait for result
			approvalResultMap, err := cb.handler.SendApprovalRequest(approvalTargets)
			if err != nil {
				cb.handler.SendError(err.Error())
				return err
			}

			// Convert approval results to targets map for resume
			targets := make(map[string]any, len(approvalResultMap))
			for id, result := range approvalResultMap {
				targets[id] = result
			}

			var resumeErr error
			streamReader, resumeErr = cb.runner.ResumeWithParams(ctx, "web", &adk.ResumeParams{
				Targets: targets,
			})
			if resumeErr != nil {
				cb.handler.SendError(resumeErr.Error())
				return resumeErr
			}
			cb.handler.SendThinking(true)
			continue
		}

		if event.Output == nil {
			continue
		}

		if event.Output.MessageOutput.Role == schema.Tool {
			cb.manager.AddMessage(ctx, event.Output.MessageOutput.Message)
			// Send message count update
			cb.handler.SendMessageCount()
			// Send completion signal for tool call using ToolCallID to find the correct index
			cb.handler.SendToolCall(
				event.Output.MessageOutput.ToolName,
				"",
				event.Output.MessageOutput.Message.ToolCallID,
				false,
			)
			// Reset firstChunk for new response after tool call
			firstChunk = true
			continue
		}

		response.Reset()
		reasoningContent.Reset()
		rawResponse.Reset()
		rawReasoning.Reset()
		toolMap := map[int][]*schema.Message{}
		if event.Output.MessageOutput.MessageStream != nil {
			reasoning, firstword := false, false
			toolStart := false
			for {
				message, err := event.Output.MessageOutput.MessageStream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					err = fmt.Errorf("error receiving message stream: %w", err)
					cb.handler.SendError(err.Error())
					return err
				}

				if len(message.ToolCalls) > 0 {
					// Only send tool call notification at the start of tool invocation
					if !toolStart {
						toolStart = true
						cb.handler.SendThinking(false)
					}
					// Accumulate and send tool calls with streaming arguments
					for i, tc := range message.ToolCalls {
						index := tc.Index
						if index == nil {
							index = &i
						}
						_, exists := toolMap[*index]
						if !exists {
							// First time seeing this tool call, send initial notification
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
							// Send initial tool call with streaming=true
							cb.handler.SendToolCall(tc.Function.Name, tc.Function.Arguments, tc.ID, true)
						} else {
							// Already sent, accumulate and send update with streaming arguments
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

							m, _ := schema.ConcatMessages(toolMap[*index])
							if len(m.ToolCalls) > 0 {
								// Send update with current accumulated arguments (streaming)
								//cb.handler.SendToolCall(m.ToolCalls[0].Function.Name, m.ToolCalls[0].Function.Arguments, m.ToolCalls[0].ID, true)
								// Send current arguments (streaming)
								cb.handler.SendToolCall(m.ToolCalls[0].Function.Name, tc.Function.Arguments, m.ToolCalls[0].ID, true)
							}
						}
					}
					// Reset firstChunk after tool call for new response content
					firstChunk = true
				}

				// Handle thinking/reasoning content
				if message.ReasoningContent != "" && !reasoning {
					cb.handler.SendThinking(true)
					reasoning = true
				}

				// Decode JSON-encoded ReasoningContent (e.g. from OpenRouter)
				if message.ReasoningContent != "" {
					rawReasoning.WriteString(message.ReasoningContent)
					decodedReasoning := message.ReasoningContent
					if err := json.Unmarshal([]byte(message.ReasoningContent), &decodedReasoning); err != nil {
						decodedReasoning = message.ReasoningContent
					}
					// Skip whitespace-only chunks at the beginning (before any meaningful content)
					if reasoningContent.Len() > 0 || strings.TrimSpace(decodedReasoning) != "" {
						// Strip leading whitespace from the first meaningful thinking chunk
						if reasoningContent.Len() == 0 {
							decodedReasoning = TrimLeadingWhitespace(decodedReasoning)
						}
						if decodedReasoning != "" {
							cb.handler.SendChunk(decodedReasoning, firstChunk, false, "thinking")
							firstChunk = false
						}
						reasoningContent.WriteString(decodedReasoning)
					}
				}

				// Transition from thinking to response content
				if message.Content != "" && reasoning && !firstword {
					cb.handler.SendThinking(false)
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
						if content != "" {
							cb.handler.SendChunk(content, firstChunk, false, "response")
							firstChunk = false
						}
						response.WriteString(content)
					}
				}
			}
			// Send final chunk marker to indicate stream end
			// contentType "response" indicates the end of the entire response
			cb.handler.SendChunk("", false, true, "response")
			// Ensure thinking state is reset at the end
			if reasoning {
				cb.handler.SendThinking(false)
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
					cb.handler.SendToolCall(tc.Function.Name, tc.Function.Arguments, tc.ID, false)
				}
				// Reset firstChunk after tool call
				firstChunk = true
			}
			if event.Output.MessageOutput.Message.Content != "" {
				cb.handler.SendChunk(event.Output.MessageOutput.Message.Content, firstChunk, false, "response")
				firstChunk = false
				response.WriteString(event.Output.MessageOutput.Message.Content)
				reasoningContent.WriteString(event.Output.MessageOutput.Message.ReasoningContent)
				rawResponse.WriteString(event.Output.MessageOutput.Message.Content)
				rawReasoning.WriteString(event.Output.MessageOutput.Message.ReasoningContent)
			}
			// Send final chunk marker
			cb.handler.SendChunk("", false, true, "response")
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
			// Send message count update after adding tool call message
			cb.handler.SendMessageCount()
		}
	}

	cb.handler.SendComplete("")
	cb.manager.AddMessage(ctx, &schema.Message{
		Role: schema.Assistant,
		// Raw (unfiltered) content, byte-identical to the model output, to
		// keep prompt-cache prefixes stable across rounds.
		Content:          rawResponse.String(),
		ReasoningContent: rawReasoning.String(),
	})

	// Send message count update after assistant response is complete
	cb.handler.SendMessageCount()

	return nil
}
