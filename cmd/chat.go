package cmd

import (
	"chat-agent/pkg/chatbot"
	"chat-agent/pkg/config"
	"chat-agent/pkg/manager"
	"chat-agent/pkg/providers"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat session",
	Long:  `Start an interactive agent session with the llm.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.GetConfig()
		chatName, _ := cmd.Flags().GetString("chat")
		preset, ok := cfg.Chats[chatName]
		if !ok {
			return fmt.Errorf("chat preset does not exist: %s", chatName)
		}
		providerFactory := providers.NewFactory(cfg)
		model, err := providerFactory.CreateChatModel(cmd.Context(), preset.Model)
		if err != nil {
			return err
		}
		chatbot := chatbot.NewChatBot(cmd.Context(), model, manager.NewManager(preset.System, preset.MaxMessages))
		// init readline
		rl, err := readline.NewEx(&readline.Config{
			Prompt:          "> ",
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
			case "quit", "exit", "bye":
				os.Stdout.WriteString("\nbye!\n")
				return nil
			default:
				err = chatbot.StreamChat(input)
				if err != nil {
					os.Stderr.WriteString("error: " + err.Error() + "\n")
				}
			}
		}
		return nil
	},
}

func init() {
	// Add agent subcommand to root command
	RootCmd.AddCommand(chatCmd)
	chatCmd.Flags().StringP("chat", "c", "", "Specify chat preset name (from config file chats)")
}
