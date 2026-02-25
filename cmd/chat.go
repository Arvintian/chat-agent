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

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/Arvintian/chat-agent/pkg/utils"

	"github.com/cloudwego/eino/components/tool"

	"github.com/ollama/ollama/readline"
	"github.com/spf13/cobra"
)

var (
	configPath string
)

// Global variables for chat switching functionality
var (
	availableChats  map[string]config.Chat
	currentChatName string
)

type MultilineState int

const (
	DefaultMaxIterations int = 20
	DefaultMaxRetries    int = 5
)

const (
	MultilineNone MultilineState = iota
	MultilinePrompt
)

// switchChat switches to a new chat session, closing the old one if provided
func switchChat(ctx context.Context, cfg *config.Config, chatName string, debug bool, oldSession *chatbot.ChatSession) (*chatbot.ChatSession, error) {
	if _, ok := cfg.Chats[chatName]; !ok {
		return nil, fmt.Errorf("chat preset does not exist: %s", chatName)
	}

	// Close old session if provided
	if oldSession != nil {
		if err := oldSession.Close(); err != nil {
			fmt.Printf("Error closing previous chat session: %v\n", err)
		}
	}

	return chatbot.InitChatSession(ctx, cfg, chatName, "local", debug)
}

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

		// Store available chats globally
		availableChats = cfg.Chats

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
		currentChatName = chatName

		// Initialize chat session
		session, err := chatbot.InitChatSession(cmd.Context(), cfg, chatName, "local", debug)
		if err != nil {
			return err
		}
		defer func() {
			if session != nil {
				if err := session.Close(); err != nil {
					fmt.Printf("Error closing session: %v\n", err)
				}
			}
		}()

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
		cb := chatbot.NewChatBot(context.WithValue(cmd.Context(), "debug", debug), session.Agent, session.Manager, scanner)

		// one-time task or chat
		welcome, _ := cmd.Flags().GetString("welcome")
		once, _ := cmd.Flags().GetString("once")
		if once != "" {
			err = cb.StreamChat(cmd.Context(), once)
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
				scanner.HistoryEnable()
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
				chatctx, cancel := context.WithCancel(cmd.Context())
				chatCancel = cancel
				input := strings.TrimSpace(sb.String())
				// exec terminal local start with /t, eg: `/t ls`
				if strings.HasPrefix(input, "/t ") {
					localcmd := strings.TrimSpace(strings.TrimPrefix(input, "/t"))
					if err := utils.PopenStream(chatctx, localcmd); err != nil {
						os.Stderr.WriteString("exec local cmd error: " + err.Error() + "\n")
					}
					sb.Reset()
					continue
				}
				// switch chat start with /s, eg: `/s code`
				if strings.HasPrefix(input, "/s ") {
					targetName := strings.TrimSpace(strings.TrimPrefix(input, "/s"))
					if targetName == "" {
						printChats()
					} else if newSession, err := switchChat(cmd.Context(), cfg, targetName, debug, session); err != nil {
						fmt.Printf("Error switching chat: %v\n", err)
					} else {
						session = newSession
						currentChatName = targetName
						cb = chatbot.NewChatBot(context.WithValue(cmd.Context(), "debug", debug), session.Agent, session.Manager, scanner)
						fmt.Printf("Switched to chat: %s\n", targetName)
					}
					sb.Reset()
					continue
				}

				switch input {
				case "/help", "/h":
					printHelp()
				case "/clear", "/c":
					session.Clear()
					fmt.Println("The conversation context is cleared")
				case "/summary", "/history", "/i":
					os.Stdout.WriteString(session.Manager.GetSummary())
					fmt.Println()
				case "/tools", "/l":
					printTools(session.Tools)
				case "/chat":
					printChats()
				case "/quit", "/exit", "/bye", "/q":
					os.Stdout.WriteString("bye!\n")
					return nil
				default:
					err = cb.StreamChat(chatctx, input)
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
	fmt.Println("  /chat            - List available chats")
	fmt.Println("  /s <name>        - Switch to another chat directly")
	fmt.Println("  /t <cmd>         - Execute local command")
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

// printChats prints the list of available chats
func printChats() {
	fmt.Println("Available chats:")
	for name, preset := range availableChats {
		marker := ""
		if name == currentChatName {
			marker = " (current)"
		}
		fmt.Printf("  - %s%s\n", name, marker)
		if preset.Desc != "" {
			fmt.Printf("    Description: %s\n", preset.Desc)
		}
		if preset.Model != "" {
			fmt.Printf("    Model: %s\n", preset.Model)
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
	RootCmd.PersistentFlags().StringP("welcome", "w", "Welcome to Chat-Agent", "Specify chat welcome message")
	RootCmd.Flags().String("once", "", "Prompt for one-time task")
}
