package cmd

import (
	"chat-agent/pkg/config"
	"fmt"
	"os"
	"path/filepath"

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
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Load configuration file
		_, err := config.LoadConfig(configPath)
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
}
