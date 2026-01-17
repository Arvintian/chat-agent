package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/manager"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/providers"
	skillloader "github.com/Arvintian/chat-agent/pkg/skill/loader"
	skillmw "github.com/Arvintian/chat-agent/pkg/skill/middleware"
	skilltools "github.com/Arvintian/chat-agent/pkg/skill/tools"
	"github.com/Arvintian/chat-agent/pkg/utils"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/ollama/ollama/readline"
	"github.com/spf13/cobra"
)

var (
	configPath string
)

type MultilineState int

const (
	DefaultMaxIterations int = 20
)

const (
	MultilineNone MultilineState = iota
	MultilinePrompt
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "chat-agent",
	Short: "Chat Agent CLI tool",
	Long:  `A command line interface for llm agent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := logger.Init(); err != nil {
			return err
		}
		// Load configuration file
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return err
		}
		chatName, _ := cmd.Flags().GetString("chat")
		debug, _ := cmd.Flags().GetBool("debug")

		//load default chat
		if chatName == "" {
			for name, item := range cfg.Chats {
				if item.Default {
					chatName = name
					break
				}
			}
		}
		if chatName == "" {
			return fmt.Errorf("Please specify the chat")
		}
		preset, ok := cfg.Chats[chatName]
		if !ok {
			return fmt.Errorf("chat preset does not exist: %s", chatName)
		}

		// chatmodel
		providerFactory := providers.NewFactory(cfg)
		model, err := providerFactory.CreateChatModel(cmd.Context(), preset.Model)
		if err != nil {
			return err
		}

		var tools []tool.BaseTool
		systemPrompt := preset.System

		// skills
		if preset.Skill != nil {
			skillDir, err := utils.ExpandPath(preset.Skill.Dir)
			if err != nil {
				return err
			}
			registry := skillloader.NewRegistry(skillloader.NewLoader(
				skillloader.WithProjectSkillsDir(skillDir),
			))
			if err := registry.Initialize(cmd.Context()); err != nil {
				return err
			}
			systemPrompt = skillmw.NewSkillsMiddleware(registry).InjectPrompt(systemPrompt)
			skillstools := skilltools.NewSkillTools(registry)
			if preset.Skill.Timeout <= 0 {
				preset.Skill.Timeout = 30
			}
			cmdTool := skilltools.RunTerminalCommandTool{
				WorkingDir: preset.Skill.WorkDir,
				Timeout:    time.Duration(preset.Skill.Timeout) * time.Second,
			}
			if preset.Skill.AutoApproval {
				skillstools = append(skillstools, &cmdTool)
			} else {
				skillstools = append(skillstools, mcp.InvokableApprovableTool{InvokableTool: &cmdTool})
			}
			tools = append(tools, skillstools...)
		}

		// mcp client
		toolLoadTimeout, _ := cmd.Flags().GetInt("tools-load-timeout")
		toolsChan, errChan := make(chan []tool.BaseTool, 1), make(chan error, 1)
		go func() {
			mcpclient := mcp.NewClient(cfg)
			if err := mcpclient.InitializeForChat(cmd.Context(), preset); err != nil {
				toolsChan <- nil
				errChan <- err
			}
			tools := mcpclient.GetToolListForServers(preset.MCPServers)
			toolsChan <- tools
			errChan <- nil
		}()
		select {
		case <-time.After(time.Duration(toolLoadTimeout) * time.Second):
			return fmt.Errorf("load mcp tools timeout")
		case err := <-errChan:
			if err != nil {
				return err
			}
			mcptools := <-toolsChan
			tools = append(tools, mcptools...)
		}

		// init agent
		maxIterations := DefaultMaxIterations
		if preset.MaxIterations > 0 {
			maxIterations = preset.MaxIterations
		}
		agent, err := adk.NewChatModelAgent(cmd.Context(), &adk.ChatModelAgentConfig{
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
		})
		if err != nil {
			return err
		}

		// init readline
		placeholder := "Send a message (/h for help)"
		scanner, err := readline.New(readline.Prompt{
			Prompt:         ">>> ",
			AltPrompt:      "... ",
			Placeholder:    placeholder,
			AltPlaceholder: `Use """ to end multi-line input`,
		})
		if err != nil {
			return err
		}
		fmt.Print(readline.StartBracketedPaste)
		defer fmt.Printf(readline.EndBracketedPaste)

		// init chatbot
		manager := manager.NewManager(preset.MaxMessages)
		chatbot := chatbot.NewChatBot(context.WithValue(cmd.Context(), "debug", debug), agent, manager, scanner)

		// one-time task or chat
		welcome, _ := cmd.Flags().GetString("welcome")
		once, _ := cmd.Flags().GetString("once")
		if once != "" {
			err = chatbot.StreamChat(cmd.Context(), once)
			if err != nil {
				os.Stderr.WriteString("\nerror: " + err.Error() + "\n")
			}
			return nil
		} else {
			fmt.Printf("%s\n", welcome)
		}

		// chat loop
		var chatCancel context.CancelFunc = func() {}
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			for {
				<-sigChan
				chatCancel() // ignore ctrl+c and break llm generate
			}
		}()
		var sb strings.Builder
		var multiline MultilineState
		for {
			if scanner.Prompt.Placeholder != placeholder {
				scanner.Prompt.Placeholder = placeholder
			}
			line, err := scanner.Readline()
			switch {
			case errors.Is(err, io.EOF):
				fmt.Println()
				return nil
			case errors.Is(err, readline.ErrInterrupt):
				if line == "" {
					fmt.Println("\nUse Ctrl + d or /q to exit.")
				}

				scanner.Prompt.UseAlt = false
				sb.Reset()

				continue
			case err != nil:
				return err
			}

			switch {
			case multiline != MultilineNone:
				// check if there's a multiline terminating string
				before, ok := strings.CutSuffix(line, `"""`)
				sb.WriteString(before)
				if !ok {
					fmt.Fprintln(&sb)
					continue
				}
				multiline = MultilineNone
				scanner.Prompt.UseAlt = false
			case strings.HasPrefix(line, `"""`):
				line := strings.TrimPrefix(line, `"""`)
				line, ok := strings.CutSuffix(line, `"""`)
				sb.WriteString(line)
				if !ok {
					// no multiline terminating string; need more input
					fmt.Fprintln(&sb)
					multiline = MultilinePrompt
					scanner.Prompt.UseAlt = true
				}
			case scanner.Pasting:
				fmt.Fprintln(&sb, line)
				continue
			default:
				sb.WriteString(line)
			}

			if sb.Len() > 0 && multiline == MultilineNone {
				input := strings.TrimSpace(sb.String())
				// exec terminal local start with /t, eg: `/t ls`
				if strings.HasPrefix(input, "/t ") {
					localcmd := strings.TrimSpace(strings.TrimPrefix(input, "/t"))
					if err := utils.PopenStream(localcmd); err != nil {
						os.Stderr.WriteString("exec local cmd error: " + err.Error() + "\n")
					}
					sb.Reset()
					continue
				}

				switch input {
				case "/help", "/h":
					printHelp()
				case "/clear", "/c":
					manager.Clear()
					fmt.Println("The conversation context is cleared")
				case "/summary", "/history", "/i":
					os.Stdout.WriteString(manager.GetSummary())
					fmt.Println()
				case "/tools", "/l":
					printTools(tools)
				case "/quit", "/exit", "/bye", "/q":
					os.Stdout.WriteString("bye!\n")
					return nil
				default:
					chatctx, cancel := context.WithCancel(cmd.Context())
					chatCancel = cancel
					err = chatbot.StreamChat(chatctx, input)
					if err != nil {
						os.Stderr.WriteString("\nerror: " + err.Error() + "\n")
					}

				}
				sb.Reset()
			}
		}
	},
}

func printHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  /help    or /h   - Show this help message")
	fmt.Println("  /history or /i   - Get conversation history")
	fmt.Println("  /clear   or /c   - Clear conversation context")
	fmt.Println("  /tools   or /l   - List the loaded tools")
	fmt.Println("  /t cmd           - Execute local command")
	fmt.Println("  /exit    or /q   - Exit program")
}

func printTools(tools []tool.BaseTool) {
	for _, item := range tools {
		info, err := item.Info(context.TODO())
		if err != nil || info == nil {
			if err != nil {
				fmt.Printf("Get tool info error %v\n", err)
			}
			continue
		}
		fmt.Printf("(%s) %s", info.Name, info.Desc)
		if !strings.HasSuffix(info.Desc, "\n") {
			fmt.Print("\n")
		}
	}
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	defaultConfigPath := filepath.Join(homeDir, ".chat-agent", "config.yml")

	// Add global parameters
	RootCmd.PersistentFlags().StringVarP(&configPath, "config", "f", defaultConfigPath, "Configuration file path")
	RootCmd.PersistentFlags().BoolP("debug", "", false, "Enable debug mode")
	RootCmd.Flags().StringP("chat", "c", "", "Specify chat preset name (from config file chats)")
	RootCmd.Flags().StringP("welcome", "w", "Welcome to Chat-Agent Cli", "Specify chat welcome message")
	RootCmd.Flags().IntP("tools-load-timeout", "t", 10, "Tool loading timeout, in seconds")
	RootCmd.Flags().String("once", "", "Prompt for one-time task")
}
