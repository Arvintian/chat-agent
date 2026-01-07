package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/manager"
	"github.com/Arvintian/chat-agent/pkg/mcp"
	"github.com/Arvintian/chat-agent/pkg/providers"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

var (
	configPath string
)

const (
	DefaultMaxIterations int = 20
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "chat-agent",
	Short: "Chat Agent CLI tool",
	Long:  `A command line interface for llm agent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load configuration file
		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			return err
		}
		chatName, _ := cmd.Flags().GetString("chat")
		debug, _ := cmd.Flags().GetBool("debug")
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
		// mcp client
		mcpclient := mcp.NewClient(cfg)
		if err := mcpclient.Initialize(cmd.Context()); err != nil {
			return err
		}
		// init agent
		tools := mcpclient.GetToolListForServers(preset.MCPServers)
		maxIterations := DefaultMaxIterations
		if preset.MaxIterations > 0 {
			maxIterations = preset.MaxIterations
		}
		agent, err := adk.NewChatModelAgent(cmd.Context(), &adk.ChatModelAgentConfig{
			Name:        chatName,
			Description: preset.Desc,
			Instruction: preset.System,
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
		// init chatbot
		manager := manager.NewManager(preset.MaxMessages)
		chatbot := chatbot.NewChatBot(context.WithValue(cmd.Context(), "debug", debug), agent, manager)
		// init readline
		rl, err := readline.NewEx(&readline.Config{
			Prompt:          ">>> ",
			HistoryFile:     filepath.Join(os.TempDir(), "chat-agent.history"),
			HistoryLimit:    200,
			AutoComplete:    nil,
			InterruptPrompt: "^C",
			EOFPrompt:       "exit",
		})
		if err != nil {
			return err
		}
		defer rl.Close()
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
		for {
			line, err := rl.Readline()
			if err != nil {
				if err == io.EOF {
					os.Stdout.WriteString("\nbye!\n")
					break
				}
				if err.Error() == "Interrupt" {
					continue
				}
				os.Stderr.WriteString("readline error: " + err.Error() + "\n")
				return err
			}

			input := strings.TrimSpace(line)

			if input == "" {
				continue
			}
			chatctx, cancel := context.WithCancel(cmd.Context())
			chatCancel = cancel

			switch input {
			case "/clear", "/k":
				manager.Clear()
			case "/summary", "/history", "/i":
				os.Stdout.WriteString(manager.GetSummary())
				fmt.Println()
			case "/quit", "/exit", "/bye", "/q":
				os.Stdout.WriteString("\nbye!\n")
				return nil
			default:
				err = chatbot.StreamChat(chatctx, input)
				if err != nil {
					os.Stderr.WriteString("\nerror: " + err.Error() + "\n")
				}
			}
		}

		return err
	},
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
	RootCmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "Configuration file path")
	RootCmd.PersistentFlags().BoolP("debug", "", false, "Enable debug mode")
	RootCmd.Flags().StringP("chat", "c", "", "Specify chat preset name (from config file chats)")
}
