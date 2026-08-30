package manager

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// PersistenceCallback is a callback function for single message persistence
// Each time a new message is added, this callback is invoked with that single message
type PersistenceCallback func(*schema.Message) error

// CompressionCompleteCallback is a callback function that is called after compression completes
// This allows the caller to persist the modified messages (full overwrite mode)
type CompressionCompleteCallback func([]*schema.Message) error

// CompressionProgressCallback is invoked right before the blocking summary
// model call so the caller can notify the user (e.g. print a hint on the CLI
// or push a message over WebSocket) that the request will pause while the
// history is compressed.
type CompressionProgressCallback func(ctx context.Context)

// Context overflow modes (chats.<name>.contextMode)
const (
	// ContextModeCompress is the default mode: when the window exceeds
	// maxMessageRounds, the oldest rounds are summarized by the chatmodel and
	// atomically replaced by a single summary round (a blocking model call per
	// compression event).
	ContextModeCompress = "compress"
	// ContextModeTruncate keeps the window within maxMessageRounds by dropping
	// the oldest round(s) once the limit is reached. No model call is
	// involved. The cap is enforced at snapshot time (GetMessages), so the
	// history sent to the model can never exceed maxMessageRound rounds.
	ContextModeTruncate = "truncate"
)

const (
	DefaultMaxMessageRound int = 10
	// minCompressRounds is the minimum number of rounds a single compression
	// event must summarize. Without it, a small maxMessageRound (e.g. 2-3)
	// triggers a blocking summary model call on every user message (compressing
	// 1-2 rounds, the window never dropping below max because the summary
	// itself occupies a round). With a minimum batch the window is allowed to
	// grow and is capped at max(2*minCompressRounds, maxMessageRound): with the
	// halving strategy len/2 reaches the batch no later than len = 2*batch, so
	// compression happens every ~batch user messages instead of every message.
	minCompressRounds = 3
)

// Manager manages conversation context.
//
// Append-only principle: once a message has been added (and therefore may have
// been sent to the model), it is never modified afterwards. The history only
// grows by appending at the tail. The only operations that rewrite the head
// are rare atomic events, both triggered when a new round starts:
//   - compress mode (default): the oldest rounds are atomically replaced by
//     a single summary round;
//   - truncate mode: the oldest round(s) are dropped at snapshot time so the
//     history sent to the model stays within maxMessageRound.
//
// In compress mode this keeps the longest common prefix of consecutive
// requests stable, which is what provider prompt caches (OpenAI/DeepSeek/Ark/
// Anthropic) match on: the head is rewritten only once per compression event
// instead of per model call. Truncate mode trades this away for simplicity.
type Manager struct {
	// messages stores the conversation history (append-only, never modified
	// except by the atomic head rewrite above)
	messages [][]*schema.Message

	// maxMessageRound limits the maximum number of message rounds in the context
	maxMessageRound int

	// contextMode selects the overflow strategy: compress (default) or truncate.
	// Set at construction only, so it is read lock-free after that.
	contextMode string

	round int

	// chatmodel for compressing messages when the limit is exceeded
	chatmodel model.ToolCallingChatModel

	mu sync.Mutex

	// compressing indicates if a synchronous compression is in progress
	// (guards against re-entrant triggers)
	compressing bool

	// loading indicates the manager is being restored from persistence.
	// Compression is skipped in this state: re-triggering compression during
	// load would make the init block on a summary model call, and the
	// compression-complete callback (which overwrites persistence) is wired up
	// only after loading finishes. Truncate mode is unaffected: its cap is
	// enforced at snapshot time, and no snapshot is taken while loading.
	loading bool

	// persistence callback for auto-saving messages
	persistenceCallback PersistenceCallback

	// compression complete callback for persisting modified messages after compression
	compressionCompleteCallback CompressionCompleteCallback

	// compression progress callback, invoked before the blocking summary call
	compressionProgressCallback CompressionProgressCallback

	// systemPrompt is the same prompt template the runner (agent) prepends to
	// every model request (see the agent's GenModelInput), and
	// systemPromptRenderer expands it. The compression summary call reuses both
	// so its request keeps the byte-identical head the runner's requests use,
	// which is what provider prompt caches (OpenAI/DeepSeek/Ark/Anthropic)
	// match on. Without it, the summary call — sent to the same model as a
	// different prefix — always misses the cache and cold-prefills the full
	// history it is about to compress.
	systemPrompt         string
	systemPromptRenderer func(string) (string, error)
}

// NewManager creates a new Manager instance.
//
// contextMode selects the overflow strategy (ContextModeCompress, default, or
// ContextModeTruncate); any unrecognized value falls back to compress.
func NewManager(maxMessageRound int, contextMode string) *Manager {
	if maxMessageRound <= 0 {
		maxMessageRound = DefaultMaxMessageRound
	}
	switch strings.ToLower(strings.TrimSpace(contextMode)) {
	case ContextModeTruncate:
		contextMode = ContextModeTruncate
	default:
		contextMode = ContextModeCompress
	}
	return &Manager{
		messages:            make([][]*schema.Message, 0),
		maxMessageRound:     maxMessageRound,
		contextMode:         contextMode,
		round:               0,
		chatmodel:           nil,
		compressing:         false,
		persistenceCallback: nil,
	}
}

// SetLoading toggles the loading (restore-from-persistence) state.
// While loading, IncRound skips compression.
func (m *Manager) SetLoading(loading bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loading = loading
}

// SetPersistenceCallback sets the callback for auto-saving messages
func (m *Manager) SetPersistenceCallback(cb PersistenceCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistenceCallback = cb
}

// SetCompressionCompleteCallback sets the callback that is called after compression completes
func (m *Manager) SetCompressionCompleteCallback(cb CompressionCompleteCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compressionCompleteCallback = cb
}

// SetCompressionProgressCallback sets the callback that is invoked before the
// blocking summary model call (to notify the user that compression is starting)
func (m *Manager) SetCompressionProgressCallback(cb CompressionProgressCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.compressionProgressCallback = cb
}

// SetChatModel sets the chat model for message compression
func (m *Manager) SetChatModel(chatmodel model.ToolCallingChatModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatmodel = chatmodel
}

func (m *Manager) GetChatModel() model.ToolCallingChatModel {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.chatmodel
}

// SetSystemPrompt shares the runner's system prompt with the manager.
//
// prompt is the same template passed to the agent's Instruction and renderer
// the same function the agent's GenModelInput uses to expand it, so the
// compression summary request leads with the exact same system message the
// runner sends — keeping its prefix prompt-cache friendly. An empty prompt
// or a nil renderer is a no-op: the summary call is then sent without a
// system message (as before).
func (m *Manager) SetSystemPrompt(prompt string, renderer func(string) (string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.systemPrompt = prompt
	m.systemPromptRenderer = renderer
}

// AddMessage adds a message to the context.
//
// Messages are only appended: compression is never triggered here, because the
// current round is still in progress. It is triggered from IncRound, when a
// new round starts and every existing round is complete.
func (m *Manager) AddMessage(_ context.Context, message *schema.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure we have at least one round
	if len(m.messages) == 0 {
		m.messages = append(m.messages, make([]*schema.Message, 0))
		m.round = 0
	}

	m.messages[m.round] = append(m.messages[m.round], message)

	// Auto-save single message to persistence if callback is set
	if m.persistenceCallback != nil {
		if err := m.persistenceCallback(message); err != nil {
			logger.Warn("manager", fmt.Sprintf("Failed to auto-save message: %v", err))
		}
	}
}

// IncRound starts a new round.
//
// This is the only place where compression is triggered: the just-finished
// round is complete and the new round is empty, so any summary model call
// here does not interfere with in-flight messages of the current round.
// Compression is skipped while restoring from persistence.
func (m *Manager) IncRound(ctx context.Context) {
	m.mu.Lock()

	// Note: the finished round is not validated here. GetMessages — the single
	// place every model-facing snapshot passes through — validates and cleans
	// the stored rounds in place (idempotently) before returning.
	m.messages = append(m.messages, make([]*schema.Message, 0))
	m.round = len(m.messages) - 1
	skip := m.loading
	m.mu.Unlock()

	// Compression performs a blocking model call (the caller/user waits by
	// design) and must not hold m.mu while doing so.
	// (Truncate mode needs no work here: its hard cap is enforced at the
	// model boundary in GetMessages.)
	if !skip && m.contextMode == ContextModeCompress {
		m.compressIfNeeded(ctx)
	}
}

// truncateLocked drops the oldest rounds until the window is within
// maxMessageRound. No model call is involved. The caller must hold m.mu.
// Truncation is not prompt-cache friendly (the head shifts on every drop);
// that is an accepted trade-off of this mode.
func (m *Manager) truncateLocked() {
	if len(m.messages) <= m.maxMessageRound {
		return
	}
	for len(m.messages) > m.maxMessageRound {
		m.messages = m.messages[1:]
	}
	m.round = len(m.messages) - 1

	// Persist the truncated history (full overwrite). Without this the JSONL
	// file still contains the dropped rounds, so after a restart the restored
	// history differs from what was last sent to the model (and would even
	// exceed the window). Normalize first so the overwrite is byte-identical
	// to the tool-call-ordered history the model actually saw (matters on the
	// first turn after a restart, before any GetMessages has sorted in memory).
	m.normalizeRoundsLocked()
	if m.compressionCompleteCallback != nil {
		if err := m.compressionCompleteCallback(flattenRounds(m.messages)); err != nil {
			logger.Warn("manager", fmt.Sprintf("Failed to persist messages after truncate: %v", err))
		}
	}
}

// validateAndCleanRound validates that tool messages and toolcalls are paired correctly
// Returns cleaned message slice with mismatched messages removed
func (m *Manager) validateAndCleanRound(messages []*schema.Message) []*schema.Message {
	// Collect all toolcall IDs from assistant messages
	toolcallIDs := make(map[string]bool)
	for _, msg := range messages {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					toolcallIDs[tc.ID] = true
				}
			}
		}
	}

	// Collect all tool response messages with their ToolCallID
	toolResponses := make(map[string]*schema.Message)
	for _, msg := range messages {
		if msg.Role == schema.Tool && msg.ToolCallID != "" {
			toolResponses[msg.ToolCallID] = msg
		}
	}

	// Identify mismatched toolcalls (no corresponding tool response)
	unmatchedToolcalls := make(map[string]bool)
	for id := range toolcallIDs {
		if _, exists := toolResponses[id]; !exists {
			unmatchedToolcalls[id] = true
		}
	}

	// Identify mismatched tool responses (no corresponding toolcall)
	unmatchedToolResponses := make(map[string]bool)
	for id := range toolResponses {
		if _, exists := toolcallIDs[id]; !exists {
			unmatchedToolResponses[id] = true
		}
	}

	// If no mismatches, return original messages
	if len(unmatchedToolcalls) == 0 && len(unmatchedToolResponses) == 0 {
		return messages
	}

	// Filter out mismatched messages
	validMessages := make([]*schema.Message, 0, len(messages))
	for _, msg := range messages {
		keep := true

		if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
			// Filter toolcalls: only keep those that have matching tool responses
			var matchedToolCalls []schema.ToolCall
			for _, tc := range msg.ToolCalls {
				if !unmatchedToolcalls[tc.ID] {
					matchedToolCalls = append(matchedToolCalls, tc)
				}
			}

			if len(matchedToolCalls) == 0 {
				// All toolcalls are unmatched, remove this assistant message
				keep = false
			} else if len(matchedToolCalls) < len(msg.ToolCalls) {
				// Some toolcalls are unmatched, create a new message with only matched ones.
				// Copy the full struct (not just Role/Content/ToolCalls) so the
				// message keeps every field the model may have seen (ReasoningContent,
				// Name, Extra, multimodal content, ...), preserving byte-identical
				// request prefixes for provider prompt caches.
				newMsg := *msg
				newMsg.ToolCalls = matchedToolCalls
				validMessages = append(validMessages, &newMsg)
			} else {
				// All toolcalls are matched, keep the original message
				validMessages = append(validMessages, msg)
			}
		} else if msg.Role == schema.Tool && msg.ToolCallID != "" {
			// Check if this tool response is unmatched
			if unmatchedToolResponses[msg.ToolCallID] {
				keep = false
			}
			if keep {
				validMessages = append(validMessages, msg)
			}
		} else {
			// Non-tool messages are always kept
			validMessages = append(validMessages, msg)
		}
	}

	return validMessages
}

// orderToolMessages reorders tool-result messages so that, for every
// assistant message carrying tool_calls, the immediately following tool
// messages appear in the same order as the tool_calls that produced them.
//
// Tool results are appended in parallel completion order (whichever tool
// finished first), which is nondeterministic and can differ from the order
// the model was actually fed. Normalizing here — at the single model-facing
// boundary — keeps consecutive request prefixes byte-identical and stable
// across restarts, which is what provider prompt caches match on.
//
// The function is idempotent and safe on incomplete rounds: tool messages
// whose ID is not present in the preceding assistant message's tool_calls
// (e.g. a truncated or aborted round) are preserved, appended in their
// original relative order after the matched ones. Non-tool messages and
// assistant/user messages are left in place; only the contiguous tool block
// directly after a tool-calling assistant message is reordered.
//
// Fast path: a round that is already in canonical order is returned as-is
// (no allocation, no copy). GetMessages writes the result back into
// m.messages[i], so once a round is fixed it stays canonical and every later
// snapshot only pays for the allocation-free scan in toolBlockNeedsReorder;
// the allocating reorder runs at most once per round.
func orderToolMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) < 2 {
		return messages
	}

	if !toolBlockNeedsReorder(messages) {
		return messages
	}

	result := make([]*schema.Message, 0, len(messages))
	i := 0
	for i < len(messages) {
		msg := messages[i]
		result = append(result, msg)

		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			i++
			continue
		}
		i++

		// Collect the contiguous block of tool messages following this
		// assistant message.
		var tools []*schema.Message
		for i < len(messages) && messages[i].Role == schema.Tool {
			tools = append(tools, messages[i])
			i++
		}
		if len(tools) <= 1 {
			result = append(result, tools...)
			continue
		}

		byID := make(map[string]*schema.Message, len(tools))
		placed := make(map[string]bool, len(tools))
		for _, t := range tools {
			if t.ToolCallID != "" {
				byID[t.ToolCallID] = t
			}
		}

		// Emit matched tools in tool-call order.
		var ordered []*schema.Message
		for _, tc := range msg.ToolCalls {
			if t, ok := byID[tc.ID]; ok {
				ordered = append(ordered, t)
				placed[tc.ID] = true
			}
		}
		// Preserve any unmatched tools (incomplete round) in original order.
		for _, t := range tools {
			if t.ToolCallID == "" || !placed[t.ToolCallID] {
				ordered = append(ordered, t)
			}
		}

		result = append(result, ordered...)
	}
	return result
}

// toolBlockNeedsReorder reports whether any tool-result block in the round is
// out of the canonical tool-call order. It is a single allocation-free pass
// (ID comparisons only) so it can run on every model-facing snapshot cheaply,
// letting orderToolMessages skip the allocating reorder in the common
// already-ordered case.
//
// Canonical target for a block: every matched tool in tool-call order first,
// then any unmatched tools. Walking the block left-to-right while consuming
// the assistant message's tool_calls in order, the first tool that is not the
// next expected matched tool is acceptable only once all matched tools have
// been placed (i.e. the rest are unmatched).
func toolBlockNeedsReorder(messages []*schema.Message) bool {
	i := 0
	for i < len(messages) {
		msg := messages[i]
		i++
		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}
		start := i
		for i < len(messages) && messages[i].Role == schema.Tool {
			i++
		}
		block := messages[start:i]
		if len(block) <= 1 {
			continue
		}

		k := 0
		for _, t := range block {
			if k < len(msg.ToolCalls) && t.ToolCallID == msg.ToolCalls[k].ID {
				k++
				continue
			}
			if k == len(msg.ToolCalls) {
				continue // all matched tools placed; remaining are unmatched
			}
			return true // a matched tool is still due before this one
		}
	}
	return false
}

// compressIfNeeded compresses the oldest rounds when the window exceeds
// maxMessageRound.
//
// The oldest rounds are summarized by the chatmodel synchronously (the caller
// waits) and atomically replaced by a single summary round — one head rewrite
// per compression event. If the summary call fails, the full history is kept
// and the compression is retried on the next trigger.
func (m *Manager) compressIfNeeded(ctx context.Context) {
	m.mu.Lock()
	if m.chatmodel == nil {
		m.mu.Unlock()
		return
	}
	if len(m.messages) <= m.maxMessageRound || m.compressing {
		m.mu.Unlock()
		return
	}
	// Never compress the current (empty, just started) round
	numToCompress := len(m.messages) / 2
	if numToCompress > len(m.messages)-1 {
		numToCompress = len(m.messages) - 1
	}
	// Below the minimum batch, skip: a summary call for 1-2 rounds is rarely
	// worth the latency it adds to the user's request. The window keeps
	// growing until the halved size reaches the batch (bounded, see
	// minCompressRounds), so compression is retried within a few rounds.
	if numToCompress < minCompressRounds {
		m.mu.Unlock()
		return
	}
	m.compressing = true
	// Normalize before snapshotting: the summary model call leads with
	// flattenRounds(oldRounds) and the persistence below leads with
	// flattenRounds(m.messages). Both must be byte-identical to the
	// tool-call-ordered history the runner feeds the model so the summary call
	// hits the prompt cache and the overwrite stays canonical. Matters on the
	// first turn after a restart, before any GetMessages has sorted in memory.
	m.normalizeRoundsLocked()
	oldRounds := m.messages[:numToCompress]
	// Snapshot the shared system prompt under the lock; doCompression runs
	// lock-free, so it must not read these fields directly.
	sysPrompt := m.systemPrompt
	sysRenderer := m.systemPromptRenderer
	m.mu.Unlock()

	// Notify the user that the request will pause for the (blocking) summary
	// call. Invoked outside the lock so the callback may do I/O freely.
	if m.compressionProgressCallback != nil {
		m.compressionProgressCallback(ctx)
	}

	// Synchronous compression: the caller (and thus the user) waits for the
	// summary model call. The lock is released while calling the model so that
	// reader operations are not blocked for the whole compression.
	summary := m.doCompression(ctx, flattenRounds(oldRounds), sysPrompt, sysRenderer)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.compressing = false

	if summary == "" {
		// Compression failed; keep the full history and retry on the next
		// trigger (window may temporarily exceed the limit).
		return
	}

	// Guard against concurrent Clear(): if the head has changed while the model
	// call was in flight, do not apply the stale result.
	if len(m.messages) < numToCompress || len(m.messages[0]) == 0 || len(oldRounds[0]) == 0 || m.messages[0][0] != oldRounds[0][0] {
		return
	}

	// Atomic head rewrite: drop the compressed rounds and insert the summary
	// round. Any previous summary round is inside oldRounds and thus gets
	// re-summarized/merged into the new one.
	m.messages = append([][]*schema.Message{
		{schema.AssistantMessage(fmt.Sprintf("[Previous Conversation Summary]: %s", summary), nil)},
	}, m.messages[numToCompress:]...)
	m.round = len(m.messages) - 1

	// Persist modified messages after compression (full overwrite)
	if m.compressionCompleteCallback != nil {
		allMessages := flattenRounds(m.messages)
		if err := m.compressionCompleteCallback(allMessages); err != nil {
			logger.Warn("manager", fmt.Sprintf("Failed to persist messages after compression: %v", err))
		}
	}
}


// doCompression performs the actual compression logic.
//
// systemPrompt/systemPromptRenderer are the runner's (the agent's
// GenModelInput) system prompt template and its renderer. When set, the
// summary request is built to mirror the runner's requests exactly:
// [system message, compressed rounds..., summarize instruction]. Since the
// runner's latest request was [system message, round1, round2, ...], this
// prefix is byte-identical, so provider prompt caches hit through the whole
// compressed history instead of cold-prefilling it.
func (m *Manager) doCompression(ctx context.Context, flatMessages []*schema.Message, systemPrompt string, systemPromptRenderer func(string) (string, error)) string {
	if len(flatMessages) == 0 {
		return ""
	}

	// Generate summary using chatmodel with inherited context
	summaryMsgs := make([]*schema.Message, 0, len(flatMessages)+1)

	// Build the shared head: the rendered system prompt leads the request,
	// absorbing any system-role messages from the history — the same merge the
	// runner's GenModelInput performs, keeping the prefix byte-identical.
	var sp *schema.Message
	if systemPrompt != "" {
		content := systemPrompt
		if systemPromptRenderer != nil {
			rendered, err := systemPromptRenderer(content)
			if err != nil {
				logger.GetDefaultLogger().Errorf("Context Manager: render system prompt failed: %v", err)
				return ""
			}
			content = rendered
		}
		sp = schema.SystemMessage(content)
	}

	for _, msg := range flatMessages {
		if sp != nil && msg.Role == schema.System {
			sp.Content = fmt.Sprintf("%s\n%s", sp.Content, msg.Content)
			continue
		}
		summaryMsgs = append(summaryMsgs, msg)
	}
	if sp != nil {
		summaryMsgs = append([]*schema.Message{sp}, summaryMsgs...)
	}

	summaryMsgs = append(summaryMsgs, schema.UserMessage("Summarize the following conversation concisely while preserving key information, decisions, and context. Output only the summary."))

	stream, err := m.chatmodel.Generate(ctx, summaryMsgs)
	if err != nil {
		logger.GetDefaultLogger().Errorf("Context Manager %v", err)
		return ""
	}

	summaryContent := strings.TrimSpace(stream.Content)
	if summaryContent == "" {
		return "Conversation summarized."
	}

	return summaryContent
}

// flattenRounds flattens rounds into a single message slice
func flattenRounds(rounds [][]*schema.Message) []*schema.Message {
	flat := make([]*schema.Message, 0)
	for _, round := range rounds {
		flat = append(flat, round...)
	}
	return flat
}

// normalizeRoundsLocked validates and sorts every round in place. The caller
// must hold m.mu.
//
// It is idempotent and cheap in the steady state: a round that is already
// clean and in canonical tool-call order passes through the allocation-free
// fast paths untouched, so repeated calls cost only a scan. Because it writes
// the normalized rounds back, a round stays canonical once fixed.
//
// Every path that reads m.messages for a model-facing snapshot (GetMessages)
// or a persistence overwrite (compress/truncate) must normalize first, so the
// result is byte-identical to the tool-call-ordered history the runner feeds
// the model — this is what keeps provider prompt caches matching.
func (m *Manager) normalizeRoundsLocked() {
	for i, round := range m.messages {
		if len(round) > 0 {
			m.messages[i] = orderToolMessages(m.validateAndCleanRound(round))
		}
	}
}

// GetMessages retrieves the messages in the current context.
// All rounds are returned in full (append-only: no simplification of older
// rounds), so the sequence of consecutive requests shares a stable, growing
// prefix that provider prompt caches can match.
// The returned messages are guaranteed to have proper tool_call / tool_result pairing.
func (m *Manager) GetMessages() []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Truncate mode enforces its hard cap right here, at the model boundary:
	// regardless of the caller's IncRound/AddMessage ordering, the history
	// sent to the model can never exceed maxMessageRound rounds.
	if m.contextMode == ContextModeTruncate {
		m.truncateLocked()
	}

	// Validate each round in place (idempotent). Unpaired tool_call/tool
	// entries can only exist in a round left incomplete by an aborted request
	// (error, cancellation, denied approval); cleaning here — the single place
	// every model-facing snapshot passes through — both guarantees the returned
	// history is clean and progressively repairs the stored rounds, so no
	// caller needs a separate validation pass.
	//
	// Tool-result order is also normalized here (see normalizeRoundsLocked):
	// tool messages are appended in parallel completion order while the model
	// is fed them in the assistant message's tool-call order. The same call is
	// made from the compression and truncate persistence paths so every
	// model-facing snapshot and every persistence overwrite is byte-identical
	// to the live request.
	m.normalizeRoundsLocked()
	return flattenRounds(m.messages)
}

// GetFullMessages retrieves all full messages in the current context
// This includes all original messages without any simplification.
// The returned messages are guaranteed to have proper tool_call / tool_result pairing.
func (m *Manager) GetFullMessages() []*schema.Message {
	return m.GetMessages()
}

// Clear clears the context (preserves system messages)
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.round = 0
	m.messages = make([][]*schema.Message, 0)
}

// RemoveLastRound removes the last round of messages from the context.
// This is used for regenerating a response - the last assistant response
// is removed so the user message can be re-processed.
func (m *Manager) RemoveLastRound() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return
	}

	m.messages = m.messages[:len(m.messages)-1]
	if m.round >= len(m.messages) {
		m.round = len(m.messages) - 1
	}
}

// GetLastUserMessage returns the content of the last user message in the conversation.
// Returns empty string if no user message is found.
func (m *Manager) GetLastUserMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return ""
	}

	// Search from the last round backwards
	for i := len(m.messages) - 1; i >= 0; i-- {
		// Search messages within the round backwards
		for j := len(m.messages[i]) - 1; j >= 0; j-- {
			if m.messages[i][j].Role == schema.User {
				return m.messages[i][j].Content
			}
		}
	}
	return ""
}

// GetMessageCount returns the total number of messages in the context
func (m *Manager) GetMessageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, round := range m.messages {
		count += len(round)
	}
	return count
}

// GetSummary generates a summary of the conversation
func (m *Manager) GetSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.messages) == 0 {
		return "Empty conversation"
	}

	var userMessages, assistantMessages, toolMessages int
	for _, round := range m.messages {
		for _, msg := range round {
			switch msg.Role {
			case schema.User:
				userMessages++
			case schema.Assistant:
				assistantMessages++
			case schema.Tool:
				toolMessages++
			}
		}
	}

	return fmt.Sprintf("Conversation contains %d user messages, %d assistant, %d tool replies", userMessages, assistantMessages, toolMessages)
}
