package cmd

import (
	"chat-agent/pkg/chatbot"
	"chat-agent/pkg/config"
	"chat-agent/pkg/manager"
	"chat-agent/pkg/mcp"
	"chat-agent/pkg/providers"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

var (
	configPath string
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
		})
		if err != nil {
			return err
		}
		manager := manager.NewManager(preset.MaxMessages)
		chatbot := chatbot.NewChatBot(cmd.Context(), agent, manager)
		// init readline
		rl, err := readline.NewEx(&readline.Config{
			Prompt:          ">>> ",
			HistoryFile:     "/tmp/chat-agent.tmp",
			AutoComplete:    nil,
			InterruptPrompt: "^C",
			EOFPrompt:       "exit",
		})
		if err != nil {
			return err
		}
		defer rl.Close()
		// chat loop
		for {
			line, err := rl.Readline()
			if err != nil {
				if err == io.EOF || err.Error() == "Interrupt" {
					os.Stdout.WriteString("\nbye!\n")
					break
				}
				os.Stderr.WriteString("readline error: " + err.Error() + "\n")
				return err
			}

			input := strings.TrimSpace(line)

			if input == "" {
				continue
			}

			switch input {
			case "/clear":
				manager.Clear()
			case "/summary", "/history":
				os.Stdout.WriteString(manager.GetSummary())
				fmt.Println()
			case "/quit", "/exit", "/bye":
				os.Stdout.WriteString("\nbye!\n")
				return nil
			default:
				err = chatbot.StreamChat(input)
				if err != nil {
					os.Stderr.WriteString("error: " + err.Error() + "\n")
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
	RootCmd.Flags().StringP("chat", "c", "", "Specify chat preset name (from config file chats)")
}
