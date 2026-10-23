package chatbot

import (
	"context"
	"strings"
	"testing"

	"github.com/Arvintian/chat-agent/pkg/config"
)

// windowModeCfg builds a config with a window-mode chat whose model points at
// a nonexistent provider, so InitChatSession fails at model creation — which
// only happens after the window-mode validation passed.
func windowModeCfg(chatWindow, modelWindow int) *config.Config {
	return &config.Config{
		Chats: map[string]config.Chat{
			"c": {
				Model:            "m",
				ContextMode:      "window",
				MaxContextTokens: chatWindow,
			},
		},
		Models: map[string]config.Model{
			"m": {ModelParams: config.ModelParams{Provider: "nope", Model: "m", MaxContextTokens: modelWindow}},
		},
	}
}

func TestInitChatSession_WindowModeRequiresContextSize(t *testing.T) {
	_, err := InitChatSession(context.Background(), windowModeCfg(0, 0), "c", "s", false)
	if err == nil {
		t.Fatal("expected error when window mode has no context size")
	}
	if !strings.Contains(err.Error(), "maxContextTokens is not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// The model-level maxContextTokens satisfies the window-mode check: init
// proceeds past validation and only fails later at model creation.
func TestInitChatSession_WindowModeFallsBackToModelContextSize(t *testing.T) {
	_, err := InitChatSession(context.Background(), windowModeCfg(0, 65536), "c", "s", false)
	if err == nil {
		t.Fatal("expected model creation error, got nil")
	}
	if strings.Contains(err.Error(), "maxContextTokens is not configured") {
		t.Fatalf("validation should have passed via model config: %v", err)
	}
	if !strings.Contains(err.Error(), "provider configuration does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A chat-level maxContextTokens alone satisfies the check.
func TestInitChatSession_WindowModeChatContextSize(t *testing.T) {
	_, err := InitChatSession(context.Background(), windowModeCfg(32768, 0), "c", "s", false)
	if err == nil {
		t.Fatal("expected model creation error, got nil")
	}
	if strings.Contains(err.Error(), "maxContextTokens is not configured") {
		t.Fatalf("validation should have passed via chat config: %v", err)
	}
}

// Non-window modes do not require a context size.
func TestInitChatSession_NonWindowModeWithoutContextSize(t *testing.T) {
	cfg := windowModeCfg(0, 0)
	chat := cfg.Chats["c"]
	chat.ContextMode = "compress"
	cfg.Chats["c"] = chat
	_, err := InitChatSession(context.Background(), cfg, "c", "s", false)
	if err == nil {
		t.Fatal("expected model creation error, got nil")
	}
	if strings.Contains(err.Error(), "maxContextTokens is not configured") {
		t.Fatalf("compress mode should not require a context size: %v", err)
	}
}
