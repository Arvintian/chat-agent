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

	// chatmodel
	providerFactory := providers.NewFactory(cfg)
	model, err := providerFactory.CreateChatModel(ctx, preset.Model)
	if err != nil {
		return nil, err
	}

	var tools []tool.BaseTool
	systemPrompt := preset.System

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

	// mcp client
	toolsChan, errChan := make(chan []tool.BaseTool, 1), make(chan error, 1)
	var mcpclient *mcp.Client
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

	var hookMgr *hook.HookManager
	if preset.Hooks != nil {
		hookMgr = hook.NewHookManager(preset.Hooks)
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
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        chatName,
		Description: preset.Desc,
		Instruction: systemPrompt,
		Model:       model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		},
		MaxIterations: maxIterations,
		ModelRetryConfig: &adk.ModelRetryConfig{
			MaxRetries:  maxRetries,
			IsRetryAble: utils.IsRetryAble,
		},
		GenModelInput: func(ctx context.Context, instruction string, input *adk.AgentInput) ([]adk.Message, error) {
			msgs := make([]adk.Message, 0, len(input.Messages)+1)
			if instruction != "" {
				rendered, err := renderSystemPrompt(instruction)
				if err != nil {
					return nil, err
				}
				sp := schema.SystemMessage(rendered)
				msgs = append(msgs, sp)
			}
			msgs = append(msgs, input.Messages...)

			if hookMgr != nil {
				resultMessages, err := hookMgr.OnGenModelInput(ctx, sessionID, chatName, msgs)
				if err != nil {
					logger.Warn("chatbot", fmt.Sprintf("GenModelInput hook execution failed: %v, using original messages", err))
				} else {
					// Use transformed messages from hook
					msgs = resultMessages
				}
			}

			return msgs, nil
		},
	})
	if err != nil {
		return nil, err
	}

	// init manager
	manager := manager.NewManager(preset.MaxMessages)

	return &ChatSession{
		ID:              sessionID,
		Name:            chatName,
		Preset:          preset,
		Agent:           agent,
		Manager:         manager,
		Tools:           tools,
		MCPClient:       mcpclient,
		cleanupRegistry: cleanupRegistry,
		hookManager:     hookMgr,
	}, nil
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

	if s.Manager != nil {
		s.Manager.Clear()
	}
	if s.cleanupRegistry != nil {
		s.cleanupRegistry.Execute()
	}

	return nil
}

func (s *ChatSession) OnKeep() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect messages before clearing for hook
	var messages []*schema.Message
	if s.Manager != nil {
		messages = s.Manager.GetMessages()
	}

	// Execute session clear hook with message history
	if s.hookManager != nil {
		if err := s.hookManager.OnSessionKeep(context.Background(), s.ID, s.Name, messages); err != nil {
			// Log error but don't fail the clear operation
			logger.Warn("chatbot", fmt.Sprintf("Session clear hook failed: %v", err))
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
		logger.Warn("chatbot", fmt.Sprintf("Genmodelinput hook failed: %v, using original messages", err))
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
