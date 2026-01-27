package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/Arvintian/chat-agent/cmd"
)

// Version information set during build
var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// Add version flag
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		printVersion()
		return
	}

	// Execute the main command
	cmd.Execute()
}

func printVersion() {
	fmt.Printf("chat-agent %s\n", Version)
	fmt.Printf("Build Time: %s\n", BuildTime)
	fmt.Printf("Go Version: %s\n", runtime.Version())
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
