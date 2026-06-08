package chatbot

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/hook"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/manager"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/providers"
	skillloader "github.com/Arvintian/chat-agent/pkg/skills/loader"
	skillmw "github.com/Arvintian/chat-agent/pkg/skills/middleware"
	skilltools "github.com/Arvintian/chat-agent/pkg/skills/tools"
	"github.com/Arvintian/chat-agent/pkg/store"
	builtintools "github.com/Arvintian/chat-agent/pkg/tools"
	"github.com/Arvintian/chat-agent/pkg/utils"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// cleanupRegistry is a session-level cleanup registry for managing resources
type cleanupRegistry = utils.CleanupRegistry

// ChatSession represents a chat session with its configuration
type ChatSession struct {
	ID              string
	Name            string
	Preset          config.Chat
	Agent           *adk.ChatModelAgent
	Manager         *manager.Manager
	Tools           []tool.BaseTool
	MCPClient       *mcp.Client
	persistence     *store.PersistenceStore
	cleanupRegistry *cleanupRegistry
	hookManager     *hook.HookManager
	closed          bool
	mu              sync.Mutex
}

// InitChatSession initializes a new chat session with the given chat name and session ID
func InitChatSession(ctx context.Context, cfg *config.Config, chatName string, sessionID string, debug bool) (*ChatSession, error) {
	preset, ok := cfg.Chats[chatName]
	if !ok {
		return nil, fmt.Errorf("chat preset does not exist: %s", chatName)
	}

	// Create session-level cleanup registry
	cleanupRegistry := NewCleanupRegistry()

	// Combine chatName and sessionID to create a unique key for persistence
	// This ensures different chat presets have separate persistence files even with the same sessionID
	persistenceKey := fmt.Sprintf("%s_%s", chatName, sessionID)

	// Initialize persistence store (default is enabled if not specified)
	var persistence *store.PersistenceStore
	contextPersistenceEnabled := preset.Persistence // Default to true when not set
	if contextPersistenceEnabled {
		var err error
		persistence, err = store.NewPersistenceStore(persistenceKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize persistence store: %w", err)
		}
	}

	// chatmodel
	providerFactory := providers.NewFactory(cfg)
	model, err := providerFactory.CreateChatModel(ctx, preset.Model)
	if err != nil {
		return nil, err
	}

	var tools []tool.BaseTool
	systemPrompt, err := config.ResolveSystemPrompt(cfg, preset.System)
	if err != nil {
		return nil, err
	}

	// builtin tools
	for _, builtinTool := range preset.Tools {
		toolCfg, ok := cfg.Tools[builtinTool]
		if !ok {
			return nil, fmt.Errorf("tool config %s not found", builtinTool)
		}
		builtinToolList, err := builtintools.GetBuiltinTools(context.WithValue(ctx, "cleanup", cleanupRegistry), toolCfg.Category, toolCfg.Params)
		if err != nil {
			return nil, err
		}
		// Check if tool category is exempt from approval (defined in pkg/tools)
		if slices.Contains(builtintools.ExemptAutoApprovalTools, toolCfg.Category) {
			tools = append(tools, builtinToolList...)
		} else if toolCfg.AutoApproval {
			tools = append(tools, builtinToolList...)
		} else {
			for _, item := range builtinToolList {
				info, err := item.Info(ctx)
				if err != nil {
					return nil, err
				}
				if slices.Contains(toolCfg.AutoApprovalTools, info.Name) {
					tools = append(tools, item)
				} else {
					tools = append(tools, mcp.InvokableApprovableTool{InvokableTool: item.(tool.InvokableTool)})
				}
			}
		}
	}

	// skills
	if preset.Skill != nil {
		skillDir, err := utils.ExpandPath(preset.Skill.Dir)
		if err != nil {
			return nil, err
		}
		registry := skillloader.NewRegistry(skillloader.NewLoader(
			skillloader.WithProjectSkillsDir(skillDir),
		))
		if err := registry.Initialize(ctx); err != nil {
			return nil, err
		}
		systemPrompt = skillmw.NewSkillsMiddleware(registry).InjectPrompt(systemPrompt)
		skillstools := skilltools.NewSkillTools(registry)
		if preset.Skill.Timeout <= 0 {
			preset.Skill.Timeout = 30
		}
		if preset.Skill.AutoApproval {
			tools = append(tools, skillstools...)
		} else {
			for _, item := range skillstools {
				info, err := item.Info(ctx)
				if err != nil {
					return nil, err
				}
				if slices.Contains(preset.Skill.AutoApprovalTools, info.Name) {
					tools = append(tools, item)
				} else {
					tools = append(tools, mcp.InvokableApprovableTool{InvokableTool: item.(tool.InvokableTool)})
				}
			}
		}
	}

	// mcp client - only initialize if MCP servers are configured
	var mcpclient *mcp.Client
	if len(preset.MCPServers) > 0 {
		toolsChan, errChan := make(chan []tool.BaseTool, 1), make(chan error, 1)
		go func() {
			mcpclient = mcp.NewClient(cfg)
			if err := mcpclient.InitializeForChat(ctx, preset); err != nil {
				toolsChan <- nil
				errChan <- err
			}
			mcptools := mcpclient.GetToolListForServers(preset.MCPServers)
			toolsChan <- mcptools
			errChan <- nil
		}()
		select {
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("load mcp tools timeout")
		case err := <-errChan:
			if err != nil {
				return nil, err
			}
			mcptools := <-toolsChan
			tools = append(tools, mcptools...)
		}
	}

	var hookMgr *hook.HookManager
	if preset.Hooks != nil {
		hookMgr = hook.NewHookManager(preset.Hooks)
	}

	toolSchemas := make([]*schema.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		schema, err := tool.Info(ctx)
		if err != nil {
			return nil, err
		}
		toolSchemas = append(toolSchemas, schema)
	}

	// init agent
	maxIterations := 20
	if preset.MaxIterations > 0 {
		maxIterations = preset.MaxIterations
	}
	maxRetries := 5
	if preset.MaxRetries > 0 {
		maxRetries = preset.MaxRetries
	}
	agentConfig := &adk.ChatModelAgentConfig{
		Name:        chatName,
		Description: preset.Desc,
		Instruction: systemPrompt,
		Model:       model,
		MaxIterations: maxIterations,
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries:  maxRetries,
			IsRetryAble: utils.IsRetryAble,
		},
		GenModelInput: func(ctx context.Context, instruction string, input *adk.AgentInput) ([]adk.Message, error) {
			var inputMessages []*schema.Message
			var err error
			inputMessages = input.Messages
			if hookMgr != nil {
				inputMessages, err = hookMgr.OnGenModelInput(ctx, sessionID, chatName, input.Messages)
				if err != nil {
					logger.Warn("chatbot", fmt.Sprintf("GenModelInput hook execution failed: %v, using original messages", err))
				}
			}
			msgs := make([]adk.Message, 0, len(input.Messages)+1)
			rendered, err := renderSystemPrompt(instruction)
			if err != nil {
				return nil, err
			}
			sp := schema.SystemMessage(rendered)
			for _, msg := range inputMessages {
				if msg.Role == schema.System {
					sp.Content = fmt.Sprintf("%s\n%s", sp.Content, msg.Content)
					continue
				}
				msgs = append(msgs, msg)
			}
			msgs = append([]adk.Message{sp}, msgs...)
			return msgs, nil
		},
	}
	// Only configure tools if there are any, to avoid "no tools to bind" error
	// from models that don't accept empty tool lists
	if len(tools) > 0 {
		agentConfig.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		}
	}
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		return nil, err
	}

	// init manager
	contextModel, err := providerFactory.CreateChatModel(ctx, preset.Model)
	if err != nil {
		return nil, err
	}
	// Only bind tools to the context model if there are any, to avoid "no tools to bind" error
	if len(toolSchemas) > 0 {
		contextModel, err = contextModel.WithTools(toolSchemas)
		if err != nil {
			return nil, err
		}
	}
	manager := manager.NewManager(preset.MaxMessageRounds)
	manager.SetChatModel(contextModel)
	if preset.FullMessageRounds > 0 {
		manager.SetFullMessageRounds(preset.FullMessageRounds)
	}

	// Only setup persistence callbacks and load messages if persistence is enabled
	if contextPersistenceEnabled {
		// Define persistence callback for later use
		persistenceCallback := func(msg *schema.Message) error {
			return persistence.SaveMessage(msg)
		}

		// Set compression complete callback for full overwrite when compression completes
		compressionCompleteCallback := func(messages []*schema.Message) error {
			return persistence.SaveMessagesOverwrite(messages)
		}

		// Load persisted messages if any (without triggering persistence callback)
		persistedMessages, err := persistence.LoadMessages()
		var loadedMessageCount int
		if err != nil {
			logger.Warn("chatbot", fmt.Sprintf("Failed to load persisted messages: %v", err))
		} else if len(persistedMessages) > 0 {
			// Temporarily set callback to nil to avoid re-saving loaded messages
			manager.SetPersistenceCallback(nil)

			// Restore messages from persistence and reconstruct rounds based on user messages
			// Each user message indicates a new round, so we need to call IncRound before adding it
			for i, msg := range persistedMessages {
				// If this is a user message and not the first message, increment round
				if msg.Role == schema.User && i > 0 {
					manager.IncRound()
				}
				manager.AddMessage(ctx, msg)
			}
			loadedMessageCount = len(persistedMessages)

			// Re-enable persistence callback after loading
			manager.SetPersistenceCallback(persistenceCallback)

			logger.Info("chatbot", fmt.Sprintf("Loaded %d messages from persistence for session %s", loadedMessageCount, sessionID))
		} else {
			// No persisted messages, just enable the callback for future messages
			manager.SetPersistenceCallback(persistenceCallback)
		}

		// Set compression complete callback after initialization
		manager.SetCompressionCompleteCallback(compressionCompleteCallback)
	} else {
		// Persistence is disabled, set nil callbacks
		manager.SetPersistenceCallback(nil)
		manager.SetCompressionCompleteCallback(nil)
	}

	session := &ChatSession{
		ID:              sessionID,
		Name:            chatName,
		Preset:          preset,
		Agent:           agent,
		Manager:         manager,
		Tools:           tools,
		MCPClient:       mcpclient,
		persistence:     persistence,
		cleanupRegistry: cleanupRegistry,
		hookManager:     hookMgr,
	}

	return session, nil
}

// NewCleanupRegistry creates a new cleanup registry for the session
func NewCleanupRegistry() *cleanupRegistry {
	return utils.NewCleanupRegistry()
}

// Close closes the chat session and releases all resources
func (s *ChatSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var errs []error

	// Close persistence store (messages are already saved via append mode on each add)
	if s.persistence != nil {
		if err := s.persistence.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close persistence: %w", err))
		}
	}

	// Close MCP client
	if s.MCPClient != nil {
		if err := s.MCPClient.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close MCP client: %w", err))
		}
	}

	// Execute session cleanup registry
	if s.cleanupRegistry != nil {
		s.cleanupRegistry.Execute()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors occurred while closing session: %v", errs)
	}
	return nil
}

// Clear clear the current context
func (s *ChatSession) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear in-memory messages
	if s.Manager != nil {
		s.Manager.Clear()
	}

	// Clear persisted messages
	if s.persistence != nil {
		if err := s.persistence.Clear(); err != nil {
			logger.Warn("chatbot", fmt.Sprintf("Failed to clear persistence: %v", err))
		} else {
			logger.Info("chatbot", fmt.Sprintf("Cleared persistence for session %s", s.ID))
		}
	}

	if s.cleanupRegistry != nil {
		s.cleanupRegistry.Execute()
	}

	return nil
}

// RemoveLastRound removes the last round of messages (used for regenerate)
func (s *ChatSession) RemoveLastRound() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Manager != nil {
		s.Manager.RemoveLastRound()

		// Update persistence with remaining messages
		if s.persistence != nil {
			messages := s.Manager.GetFullMessages()
			if err := s.persistence.SaveMessagesOverwrite(messages); err != nil {
				logger.Warn("chatbot", fmt.Sprintf("Failed to overwrite persistence after removing last round: %v", err))
			}
		}
	}
}

// GetLastUserMessage returns the last user message from the conversation, if any.
// Used for redo/regenerate functionality.
func (s *ChatSession) GetLastUserMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Manager != nil {
		return s.Manager.GetLastUserMessage()
	}
	return ""
}

// GetMessageCount returns the number of messages in the session
func (s *ChatSession) GetMessageCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Manager == nil {
		return 0
	}
	return s.Manager.GetMessageCount()
}

// PersistenceStore returns the persistence store for this session
func (s *ChatSession) PersistenceStore() *store.PersistenceStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.persistence
}

func (s *ChatSession) OnKeep() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect full messages before clearing for hook
	var messages []*schema.Message
	if s.Manager != nil {
		messages = s.Manager.GetFullMessages()
	}

	// Execute session keep hook with full message history
	if s.hookManager != nil {
		if err := s.hookManager.OnSessionKeep(context.Background(), s.ID, s.Name, messages); err != nil {
			// Log error but don't fail the clear operation
			logger.Warn("chatbot", fmt.Sprintf("Session keep hook failed: %v", err))
		}
	}

	return nil
}

// OnGenModelInput executes the genmodelinput hook if configured
// This hook is called before sending messages to the model and can modify the message list
func (s *ChatSession) OnGenModelInput(ctx context.Context, instruction string, inputMessages []*schema.Message) ([]*schema.Message, error) {
	if s.hookManager == nil {
		return inputMessages, nil
	}

	// Execute hook directly with schema.Message
	resultMessages, err := s.hookManager.OnGenModelInput(ctx, s.ID, s.Name, inputMessages)
	if err != nil {
		// Log error but return original messages
		logger.Warn("chatbot", fmt.Sprintf("GenModelInput hook failed: %v, using original messages", err))
		return inputMessages, nil
	}

	return resultMessages, nil
}

// renderSystemPrompt renders system prompt using Go template with built-in variables
func renderSystemPrompt(systemPrompt string) (string, error) {
	if systemPrompt == "" {
		return "", nil
	}

	// Create template with built-in functions
	tmpl, err := template.New("systemPrompt").Funcs(template.FuncMap{
		"env": os.Getenv, // Allow accessing environment variables
	}).Parse(systemPrompt)
	if err != nil {
		return "", fmt.Errorf("failed to parse system prompt template: %w", err)
	}

	// Prepare template data with built-in variables
	data := struct {
		Cwd  string
		Date string
		Now  time.Time
		User string
		Home string
	}{
		Cwd:  getCurrentWorkingDir(),
		Date: time.Now().Format("2006-01-02"),
		Now:  time.Now(),
		User: getUserName(),
		Home: getHomeDir(),
	}

	// Execute template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute system prompt template: %w", err)
	}

	return buf.String(), nil
}

// getCurrentWorkingDir returns the current working directory
func getCurrentWorkingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// getUserName returns the current user name
func getUserName() string {
	if user, err := os.UserHomeDir(); err == nil {
		// Extract username from home directory path
		if parts := strings.Split(user, "/"); len(parts) > 2 {
			return parts[len(parts)-1]
		}
	}
	return "user"
}

// getHomeDir returns the user's home directory
func getHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "~"
}
