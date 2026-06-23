package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/serve"
	"github.com/Arvintian/readline"
	"github.com/hekmon/liveterm/v2"
	"github.com/spf13/cobra"
)

var (
	serverURL   string
	chatName    string
	basicAuth   string
	sessionID   string
	noReconnect bool
)

// handler implements serve.EventHandler to display server events on the terminal.
type handler struct {
	cli                *serve.Client
	chatName           string
	mu                 sync.Mutex
	thinking           bool
	thinkingHeader     bool // whether the "Thinking:" header has been printed
	lastContent        string
	lastContentType    string // content type of the last printed chunk ("thinking", "response", or "")
	awaitingApproval   bool
	approvalID         string            // stored approval ID from OnApprovalRequest
	approvalTargets    []string          // stored target IDs from OnApprovalRequest
	responseDone       chan struct{}     // signaled when a server response completes
	streamingToolArgs  map[string]string // index -> accumulated incremental arguments
	streamingToolNames map[string]string // index -> tool name (for display)
	activeToolIndices  []string          // ordered list of indices currently streaming (for liveterm multi-line)
	livetermActive     bool              // whether liveterm is currently running
}

func newHandler() *handler {
	return &handler{
		responseDone:       make(chan struct{}, 1),
		streamingToolArgs:  make(map[string]string),
		streamingToolNames: make(map[string]string),
	}
}

// signalDone sends a non-blocking signal that a response has completed.
func (h *handler) signalDone() {
	select {
	case h.responseDone <- struct{}{}:
	default:
	}
}

// drainDone drains any stale completion signal before starting a new request.
func (h *handler) drainDone() {
	select {
	case <-h.responseDone:
	default:
	}
}

// ---------------------------------------------------------------------------
// EventHandler implementation
// ---------------------------------------------------------------------------

func (h *handler) OnSessionInit(_ *serve.SessionInitPayload) {
	// CLI mode doesn't show session init; silent
}

func (h *handler) OnChatSelected(payload *serve.ChatSelectedPayload) {
	if payload.MessageCount > 0 {
		h.rawLine(fmt.Sprintf("[Context restored from previous session: %d messages]", payload.MessageCount))
	}
}

func (h *handler) OnChunk(payload *serve.ChunkPayload) {
	h.resetLiveTerm()

	h.mu.Lock()
	defer h.mu.Unlock()

	// Handle content type transitions (thinking → response)
	if payload.ContentType != h.lastContentType && h.lastContentType != "" && len(h.lastContent) > 0 {
		if !strings.HasSuffix(h.lastContent, "\n") {
			fmt.Println()
		}
		if payload.ContentType == "response" && h.lastContentType == "thinking" {
			fmt.Println("---")
			h.thinkingHeader = false
		}
	}

	// Print thinking header when starting thinking content
	if payload.ContentType == "thinking" && !h.thinkingHeader {
		fmt.Print("Thinking:\n")
		h.thinkingHeader = true
	}

	// Clear the prompt line before the very first content of a response
	if h.lastContentType == "" && !h.thinkingHeader && payload.Content != "" {
		fmt.Print("\r\033[K")
	}

	if payload.Content != "" {
		fmt.Print(payload.Content)
		h.lastContent = payload.Content
	}

	if payload.Last {
		if len(h.lastContent) > 0 && !strings.HasSuffix(h.lastContent, "\n") {
			fmt.Println()
		}
		h.lastContentType = ""
		h.lastContent = ""
		h.thinkingHeader = false
	} else if payload.Content != "" {
		h.lastContentType = payload.ContentType
	}
}

func (h *handler) OnToolCall(payload *serve.ToolCallPayload) {
	h.resetChunk()

	h.mu.Lock()
	// Accumulate incremental arguments for streaming tool calls
	if payload.Streaming {
		h.streamingToolArgs[payload.Index] = serve.ConcatToolArguments(h.streamingToolArgs[payload.Index], payload.Arguments)
		h.streamingToolNames[payload.Index] = payload.Name
	}

	streaming := payload.Streaming
	var (
		needStart bool
		printLine string // non-empty: print this static line after liveterm
	)

	if streaming {
		// Register this tool call in the active multi-line display
		found := false
		for _, idx := range h.activeToolIndices {
			if idx == payload.Index {
				found = true
				break
			}
		}
		if !found {
			h.activeToolIndices = append(h.activeToolIndices, payload.Index)
		}

		if !h.livetermActive {
			needStart = true
		}
		h.livetermActive = true
	} else {
		// Remove from active indices
		for i, idx := range h.activeToolIndices {
			if idx == payload.Index {
				h.activeToolIndices = append(h.activeToolIndices[:i], h.activeToolIndices[i+1:]...)
				break
			}
		}
		delete(h.streamingToolArgs, payload.Index)
		delete(h.streamingToolNames, payload.Index)
		line := fmt.Sprintf("ToolCall: (%s) Completed\n---", payload.Name)
		truncated, _ := chatbot.TruncateToTermWidth(line)
		printLine = truncated
	}
	h.mu.Unlock()

	if needStart {
		liveterm.RefreshInterval = 200 * time.Millisecond
		liveterm.Output = os.Stdout
		liveterm.SetMultiLinesUpdateFx(func() []string {
			h.mu.Lock()
			defer h.mu.Unlock()
			lines := make([]string, 0, len(h.activeToolIndices))
			for _, idx := range h.activeToolIndices {
				name := h.streamingToolNames[idx]
				args := h.streamingToolArgs[idx]
				// Truncate the complete accumulated line
				line, _ := chatbot.TruncateToTermWidth(fmt.Sprintf("ToolCall: (%s) %s", name, args))
				lines = append(lines, line)
				lines = append(lines, "---")
			}
			return lines
		})
		if err := liveterm.Start(); err != nil {
			h.mu.Lock()
			h.livetermActive = false
			indices := make([]string, len(h.activeToolIndices))
			copy(indices, h.activeToolIndices)
			h.mu.Unlock()
		}
	}
	if printLine != "" {
		fmt.Println(printLine)
	}
}

func (h *handler) OnThinking(payload *serve.ThinkingPayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if payload.Status && !h.thinking {
		h.thinking = true
	} else if !payload.Status && h.thinking {
		h.thinking = false
	}
}

func (h *handler) OnComplete(_ *serve.CompletePayload) {
	h.resetLiveTerm()
	h.signalDone()
}

func (h *handler) OnError(payload *serve.ErrorPayload) {
	os.Stderr.WriteString("\nerror: " + payload.Error + "\n")
	h.signalDone()
}

func (h *handler) OnApprovalRequest(payload *serve.ApprovalRequestPayload) {
	h.resetLiveTerm()

	h.mu.Lock()
	h.awaitingApproval = true
	h.approvalID = payload.ApprovalID
	h.approvalTargets = make([]string, len(payload.Targets))
	for i, t := range payload.Targets {
		h.approvalTargets[i] = t.ID
	}
	h.mu.Unlock()

	for _, t := range payload.Targets {
		line := fmt.Sprintf("Approval: (%s) waiting for your approval", t.Tool)
		truncated, _ := chatbot.TruncateToTermWidth(line)
		fmt.Println(truncated)
		if t.Details != "" {
			detailsLine := fmt.Sprintf("  Details: %s", t.Details)
			truncatedDetails, _ := chatbot.TruncateToTermWidth(detailsLine)
			fmt.Println(truncatedDetails)
		}
	}
	fmt.Printf("Respond with /approve or /deny [reason]\n")

	// Wake up the main loop so the user can respond to the approval request.
	h.signalDone()
}

func (h *handler) OnMessageCount(_ *serve.MessageCountPayload) {
	// CLI mode doesn't show message count; silent
}

func (h *handler) OnStopped(_ *serve.StoppedPayload) {
	h.resetLiveTerm()
	h.signalDone()
}

func (h *handler) OnKept(_ *serve.KeptPayload) {
	h.rawLine("Session keep hook executed successfully")
	h.signalDone()
}

func (h *handler) OnCleared(_ *serve.ClearedPayload) {
	h.rawLine("The conversation context is cleared")
	h.signalDone()
}

func (h *handler) OnDisconnected(err error) {
	if err != nil {
		h.rawLine(fmt.Sprintf("[Disconnected] %v", err))
	} else {
		h.rawLine("[Disconnected]")
	}
}

func (h *handler) OnReconnected() {
	h.rawLine("[Reconnected]")
	h.cli.SelectChat(h.chatName)
}

// isAwaitingApproval returns true if we're waiting for an approval response.
func (h *handler) isAwaitingApproval() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.awaitingApproval
}

// getApprovalID returns the stored approval ID.
func (h *handler) getApprovalID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.approvalID
}

// getApprovalTargets returns the stored approval target IDs.
func (h *handler) getApprovalTargets() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.approvalTargets
}

// resetApproval clears the awaiting-approval flag, stored approval ID and targets.
func (h *handler) resetApproval() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.awaitingApproval = false
	h.approvalID = ""
	h.approvalTargets = nil
}

func (h *handler) resetLiveTerm() {
	if h.livetermActive {
		liveterm.Stop(false)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.livetermActive = false
	h.activeToolIndices = nil
	h.streamingToolArgs = make(map[string]string)
	h.streamingToolNames = make(map[string]string)
}

func (h *handler) resetChunk() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastContentType = ""
	h.lastContent = ""
}

func (h *handler) rawLine(line string) {
	fmt.Print("\r\033[K")
	fmt.Println(line)
}

func parseBasicAuth(raw string) (user, pass string) {
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", ""
}

func printHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  /help    or /h   - Show this help message")
	fmt.Println("  /clear   or /c   - Clear conversation context")
	fmt.Println("  /keep    or /k   - Execute session keep hook")
	fmt.Println("  /stop    or /s   - Stop current response")
	fmt.Println("  /approve         - Approve all pending tool calls")
	fmt.Println("  /deny [reason]   - Deny all pending tool calls")
	fmt.Println("  /quit    or /q   - Exit program")
	fmt.Println("  /exit    or /bye - Exit program")
}

var rootCmd = &cobra.Command{
	Use:   "chat-agent-client",
	Short: "WebSocket debug client for chat-agent serve mode",
	Long: `A readline-based interactive CLI that connects to a chat-agent serve
mode WebSocket endpoint for debugging and testing.

Examples:
  chat-agent-client --url ws://localhost:8080/ws --chat mybot
  chat-agent-client --url ws://localhost:8080/ws --chat mybot --basic-auth user:pass
  chat-agent-client --url ws://localhost:8080/ws --chat mybot --session-id my-project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if serverURL == "" {
			return fmt.Errorf("--url is required (e.g. ws://localhost:8080/ws)")
		}
		if chatName == "" {
			return fmt.Errorf("--chat is required")
		}

		h := newHandler()

		// Build options
		var opts []serve.ClientOption
		if basicAuth != "" {
			user, pass := parseBasicAuth(basicAuth)
			if user != "" {
				opts = append(opts, serve.WithBasicAuth(user, pass))
			}
		}
		if sessionID != "" {
			opts = append(opts, serve.WithSessionID(sessionID))
		}
		if !noReconnect {
			opts = append(opts, serve.WithReconnect(2*time.Second, 10))
		}

		client := serve.NewClient(serverURL, opts...)
		h.cli = client
		h.chatName = chatName
		client.SetEventHandler(h)

		fmt.Printf("Connecting to %s\n", serverURL)
		if err := client.Connect(); err != nil {
			return fmt.Errorf("connect failed: %w", err)
		}
		defer client.Close()

		// Select the chat
		if err := client.SelectChat(chatName); err != nil {
			return fmt.Errorf("select chat failed: %w", err)
		}

		fmt.Printf("Connected to chat: %s\n", chatName)

		for {
			<-time.After(100 * time.Millisecond)
			if client.IsConnected() {
				break
			}
		}

		// Initialize readline
		placeholder := "Send a message (/h for help)"
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		historyPath := filepath.Join(homeDir, ".chat-agent", "client-history")
		scanner, err := readline.New(readline.Prompt{
			Prompt:      ">>> ",
			AltPrompt:   "... ",
			Placeholder: placeholder,
		}, readline.WithHistoryFile(historyPath))
		if err != nil {
			return err
		}
		scanner.UnsetRawMode()
		fmt.Print(readline.StartBracketedPaste)
		defer fmt.Printf(readline.EndBracketedPaste)

		// Handle Ctrl+C — send stop instead of exiting
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			for range sigChan {
				// If awaiting approval, don't stop on Ctrl+C
				if h.isAwaitingApproval() {
					continue
				}
				client.Stop()
			}
		}()

		var multiline bool
		var sb strings.Builder

		for {
			line, err := scanner.Readline()
			switch {
			case errors.Is(err, io.EOF):
				fmt.Println()
				return nil
			case errors.Is(err, readline.ErrInterrupt):
				if line == "" {
					fmt.Println("\nUse Ctrl+D or /q to exit.")
				}
				scanner.Prompt.UseAlt = false
				sb.Reset()
				multiline = false
				continue
			case err != nil:
				return err
			}

			switch {
			case multiline:
				before, ok := strings.CutSuffix(line, `"""`)
				sb.WriteString(before)
				if !ok {
					fmt.Fprintln(&sb)
					continue
				}
				multiline = false
				scanner.Prompt.UseAlt = false
			case strings.HasPrefix(line, `"""`):
				line := strings.TrimPrefix(line, `"""`)
				line, ok := strings.CutSuffix(line, `"""`)
				sb.WriteString(line)
				if !ok {
					fmt.Fprintln(&sb)
					multiline = true
					scanner.Prompt.UseAlt = true
				}
			case scanner.Pasting:
				fmt.Fprintln(&sb, line)
				continue
			default:
				sb.WriteString(line)
			}

			if sb.Len() > 0 && !multiline {
				input := strings.TrimSpace(sb.String())

				switch {
				case h.isAwaitingApproval():
					// Handle approval responses
					switch {
					case input == "/approve":
						approvalID := h.getApprovalID()
						targetIDs := h.getApprovalTargets()
						h.resetApproval()
						h.drainDone()

						results := make(map[string]serve.ApprovalItem, len(targetIDs))
						for _, id := range targetIDs {
							results[id] = serve.ApprovalItem{Approved: true}
						}
						client.SendApprovalResponse(approvalID, results)
						<-h.responseDone
					case strings.HasPrefix(input, "/deny"):
						reason := strings.TrimSpace(strings.TrimPrefix(input, "/deny"))
						approvalID := h.getApprovalID()
						targetIDs := h.getApprovalTargets()
						h.resetApproval()
						h.drainDone()

						results := make(map[string]serve.ApprovalItem, len(targetIDs))
						for _, id := range targetIDs {
							results[id] = serve.ApprovalItem{Approved: false, Reason: reason}
						}
						client.SendApprovalResponse(approvalID, results)
						<-h.responseDone
					default:
						fmt.Println("Approval required. Use /approve or /deny [reason]")
					}

				case input == "/help" || input == "/h":
					printHelp()
				case input == "/clear" || input == "/c":
					h.drainDone()
					client.Clear()
					<-h.responseDone
				case input == "/keep" || input == "/k":
					h.drainDone()
					client.Keep()
					<-h.responseDone
				case input == "/stop" || input == "/s":
					h.drainDone()
					client.Stop()
					<-h.responseDone
				case input == "/quit" || input == "/exit" || input == "/bye" || input == "/q":
					fmt.Println("bye!")
					return nil
				default:
					h.drainDone()
					if err := client.SendMessage(input); err != nil {
						fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					} else {
						<-h.responseDone
					}
				}

				sb.Reset()
			}
		}
	},
}

func init() {
	rootCmd.Flags().StringVarP(&serverURL, "url", "u", "", "WebSocket server URL (e.g. ws://localhost:8080/ws)")
	rootCmd.Flags().StringVarP(&chatName, "chat", "c", "", "Chat preset name to select")
	rootCmd.Flags().StringVarP(&basicAuth, "basic-auth", "a", "", "Basic auth credentials (user:pass)")
	rootCmd.Flags().StringVarP(&sessionID, "session-id", "s", "", "Session ID (for reusing sessions)")
	rootCmd.Flags().BoolVar(&noReconnect, "no-reconnect", false, "Disable automatic reconnection")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
