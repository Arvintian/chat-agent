package main

import (
	"chat-agent/cmd"
	"chat-agent/pkg/logger"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Initialize logging
	if err := logger.Init(); err != nil {
		// If logging fails, still run the app but log to stderr
		os.Stderr.WriteString("Warning: Failed to initialize logging: " + err.Error() + "\n")
	}

	// Ensure logger is closed on exit
	defer logger.Close()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("MAIN", "Received signal, shutting down: "+sig.String())
		logger.Close()
		os.Exit(0)
	}()
	// Execute the main command
	cmd.Execute()
}
