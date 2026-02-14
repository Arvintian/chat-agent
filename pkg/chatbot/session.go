package chatbot

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Arvintian/chat-agent/pkg/config"
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
)

// ChatSession represents a chat session with its configuration
type ChatSession struct {
	Name    string
	Preset  config.Chat
	Agent   *adk.ChatModelAgent
	Manager *manager.Manager
	Tools   []tool.BaseTool
}

// InitChatSession initializes a new chat session with the given chat name
func InitChatSession(ctx context.Context, cfg *config.Config, cleanupRegistry *utils.CleanupRegistry, chatName string, debug bool) (*ChatSession, error) {
	preset, ok := cfg.Chats[chatName]
	if !ok {
		return nil, fmt.Errorf("chat preset does not exist: %s", chatName)
	}

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
		if toolCfg.AutoApproval {
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

		// Auto-add cmd_bg tool when cmd tool is enabled (without approval control)
		if toolCfg.Category == "cmd" || toolCfg.Category == "smart_cmd" {
			bgToolList, err := builtintools.GetBuiltinTools(context.WithValue(ctx, "cleanup", cleanupRegistry), "cmd_bg", nil)
			if err != nil {
				return nil, err
			}
			tools = append(tools, bgToolList...)
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
	go func() {
		mcpclient := mcp.NewClient(cfg)
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
	})
	if err != nil {
		return nil, err
	}

	// init manager
	manager := manager.NewManager(preset.MaxMessages)

	return &ChatSession{
		Name:    chatName,
		Preset:  preset,
		Agent:   agent,
		Manager: manager,
		Tools:   tools,
	}, nil
}