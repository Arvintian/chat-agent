package config

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSnakeToCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"foo", "foo"},
		{"base_url", "baseUrl"},
		{"api_key", "apiKey"},
		{"mcp_servers", "mcpServers"},
		{"max_tokens", "maxTokens"},
		{"reasoning_effort", "reasoningEffort"},
		{"extra_body", "extraBody"},
		{"script_path", "scriptPath"},
		{"top_p", "topP"},
		{"top_k", "topK"},
		{"max_message_rounds", "maxMessageRounds"},
		{"full_message_rounds", "fullMessageRounds"},
		{"max_iterations", "maxIterations"},
		{"max_retries", "maxRetries"},
		{"auto_approval_tools", "autoApprovalTools"},
		{"gen_model_input", "genModelInput"},
		{"no_concurrent", "noConcurrent"},
		{"no_concurrent_tools", "noConcurrentTools"},
		{"lowercase_tools", "lowercaseTools"},
		{"system_prompts", "systemPrompts"},
		{"alreadyCamelCase", "alreadyCamelCase"},
	}

	for _, tt := range tests {
		got := snakeToCamel(tt.input)
		if got != tt.expected {
			t.Errorf("snakeToCamel(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeNodeKeys(t *testing.T) {
	node := &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{
				Kind: yaml.MappingNode,
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "base_url"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "http://example.com"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "api_key"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "sk-123"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "mcp_servers"},
					{
						Kind: yaml.MappingNode,
						Content: []*yaml.Node{
							{Kind: yaml.ScalarNode, Tag: "!!str", Value: "my_server"},
							{
								Kind: yaml.MappingNode,
								Content: []*yaml.Node{
									{Kind: yaml.ScalarNode, Tag: "!!str", Value: "auto_approval_tools"},
									{
										Kind: yaml.SequenceNode,
										Content: []*yaml.Node{
											{Kind: yaml.ScalarNode, Tag: "!!str", Value: "search"},
										},
									},
									{Kind: yaml.ScalarNode, Tag: "!!str", Value: "no_concurrent"},
									{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
									{Kind: yaml.ScalarNode, Tag: "!!str", Value: "no_concurrent_tools"},
									{
										Kind: yaml.SequenceNode,
										Content: []*yaml.Node{
											{Kind: yaml.ScalarNode, Tag: "!!str", Value: "fetch"},
										},
									},
									{Kind: yaml.ScalarNode, Tag: "!!str", Value: "lowercase_tools"},
									{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
								},
							},
						},
					},
				},
			},
		},
	}

	normalizeNodeKeys(node)

	// Extract the top-level mapping
	m := node.Content[0]
	if m.Kind != yaml.MappingNode {
		t.Fatal("expected mapping node")
	}

	keys := make(map[string]string)
	for i := 0; i < len(m.Content); i += 2 {
		keys[m.Content[i].Value] = m.Content[i+1].Value
	}

	if keys["baseUrl"] != "http://example.com" {
		t.Errorf("baseUrl = %q, want %q", keys["baseUrl"], "http://example.com")
	}
	if keys["apiKey"] != "sk-123" {
		t.Errorf("apiKey = %q, want %q", keys["apiKey"], "sk-123")
	}
	if _, ok := keys["mcpServers"]; !ok {
		t.Error("mcpServers key not found")
	}

	// Check nested keys
	mcpServers := m.Content[5]
	if mcpServers.Kind != yaml.MappingNode || len(mcpServers.Content) < 2 {
		t.Fatal("expected nested mapping for mcpServers")
	}
	serverContent := mcpServers.Content[1]
	if serverContent.Kind != yaml.MappingNode {
		t.Fatal("expected server mapping")
	}
	serverKeys := make(map[string]bool)
	for i := 0; i < len(serverContent.Content); i += 2 {
		serverKeys[serverContent.Content[i].Value] = true
	}
	if !serverKeys["autoApprovalTools"] {
		t.Error("nested autoApprovalTools key not normalized")
	}
	if !serverKeys["noConcurrent"] {
		t.Error("nested noConcurrent key not normalized")
	}
	if !serverKeys["noConcurrentTools"] {
		t.Error("nested noConcurrentTools key not normalized")
	}
	if !serverKeys["lowercaseTools"] {
		t.Error("nested lowercaseTools key not normalized")
	}
}

func TestLoadConfigCamelCase(t *testing.T) {
	// Write a temp config with camelCase keys
	tmp := t.TempDir()
	path := tmp + "/config.yml"
	data := `
providers:
  openai:
    type: openai
    baseUrl: https://api.openai.com
    apiKey: sk-test
models:
  gpt4:
    provider: openai
    model: gpt-4
    maxTokens: 4096
chats:
  default:
    model: gpt4
    system: general
    mcpServers:
      - myserver
mcpServers:
  myserver:
    type: sse
    url: http://localhost:8080
    noConcurrent: true
    noConcurrentTools:
      - search
    lowercaseTools: true
systemPrompts:
  general: "You are a helpful assistant."
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Providers["openai"].BaseURL != "https://api.openai.com" {
		t.Errorf("BaseURL = %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Models["gpt4"].MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", cfg.Models["gpt4"].MaxTokens)
	}
	if !cfg.MCPServers["myserver"].NoConcurrent {
		t.Errorf("NoConcurrent = %v", cfg.MCPServers["myserver"].NoConcurrent)
	}
	if len(cfg.MCPServers["myserver"].NoConcurrentTools) != 1 ||
		cfg.MCPServers["myserver"].NoConcurrentTools[0] != "search" {
		t.Errorf("NoConcurrentTools = %v", cfg.MCPServers["myserver"].NoConcurrentTools)
	}
	if !cfg.MCPServers["myserver"].LowercaseTools {
		t.Errorf("LowercaseTools = %v", cfg.MCPServers["myserver"].LowercaseTools)
	}
	if cfg.Chats["default"].System != "general" {
		t.Errorf("System = %q, want %q", cfg.Chats["default"].System, "general")
	}
	if cfg.SystemPrompts["general"] != "You are a helpful assistant." {
		t.Errorf("SystemPrompts[general] = %q", cfg.SystemPrompts["general"])
	}
}

func TestLoadConfigSnakeCaseCompat(t *testing.T) {
	// Write a temp config with snake_case keys for backward compatibility
	tmp := t.TempDir()
	path := tmp + "/config.yml"
	data := `
providers:
  openai:
    type: openai
    base_url: https://api.openai.com
    api_key: sk-test
models:
  gpt4:
    provider: openai
    model: gpt-4
    max_tokens: 4096
    reasoning_effort: high
chats:
  default:
    model: gpt4
    system: general
    mcp_servers:
      - myserver
    max_message_rounds: 10
    max_retries: 3
mcp_servers:
  myserver:
    type: sse
    url: http://localhost:8080
    auto_approval_tools:
      - list
    no_concurrent: true
    no_concurrent_tools:
      - search
      - fetch
    lowercase_tools: true
system_prompts:
  general: "You are a helpful assistant."
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig with snake_case failed: %v", err)
	}

	// Check top-level keys
	if _, ok := cfg.Providers["openai"]; !ok {
		t.Error("providers.openai not parsed")
	}
	if _, ok := cfg.Models["gpt4"]; !ok {
		t.Error("models.gpt4 not parsed")
	}
	if _, ok := cfg.Chats["default"]; !ok {
		t.Error("chats.default not parsed")
	}
	if _, ok := cfg.MCPServers["myserver"]; !ok {
		t.Error("mcp_servers.myserver not parsed")
	}
	if _, ok := cfg.SystemPrompts["general"]; !ok {
		t.Error("system_prompts.general not parsed")
	}

	// Check provider fields
	if cfg.Providers["openai"].BaseURL != "https://api.openai.com" {
		t.Errorf("BaseURL = %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Providers["openai"].APIKey != "sk-test" {
		t.Errorf("APIKey = %q", cfg.Providers["openai"].APIKey)
	}

	// Check model fields
	if cfg.Models["gpt4"].MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", cfg.Models["gpt4"].MaxTokens)
	}
	if cfg.Models["gpt4"].ReasoningEffort == nil || *cfg.Models["gpt4"].ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %v", cfg.Models["gpt4"].ReasoningEffort)
	}

	// Check chat fields
	if cfg.Chats["default"].MaxMessageRounds != 10 {
		t.Errorf("MaxMessageRounds = %d", cfg.Chats["default"].MaxMessageRounds)
	}
	if cfg.Chats["default"].MaxRetries != 3 {
		t.Errorf("MaxRetries = %d", cfg.Chats["default"].MaxRetries)
	}
	if len(cfg.Chats["default"].MCPServers) != 1 || cfg.Chats["default"].MCPServers[0] != "myserver" {
		t.Errorf("MCPServers = %v", cfg.Chats["default"].MCPServers)
	}

	// Check mcp server fields
	if len(cfg.MCPServers["myserver"].AutoApprovalTools) != 1 ||
		cfg.MCPServers["myserver"].AutoApprovalTools[0] != "list" {
		t.Errorf("AutoApprovalTools = %v", cfg.MCPServers["myserver"].AutoApprovalTools)
	}
	if !cfg.MCPServers["myserver"].NoConcurrent {
		t.Errorf("NoConcurrent = %v", cfg.MCPServers["myserver"].NoConcurrent)
	}
	if len(cfg.MCPServers["myserver"].NoConcurrentTools) != 2 ||
		cfg.MCPServers["myserver"].NoConcurrentTools[0] != "search" ||
		cfg.MCPServers["myserver"].NoConcurrentTools[1] != "fetch" {
		t.Errorf("NoConcurrentTools = %v", cfg.MCPServers["myserver"].NoConcurrentTools)
	}
	if !cfg.MCPServers["myserver"].LowercaseTools {
		t.Errorf("LowercaseTools = %v", cfg.MCPServers["myserver"].LowercaseTools)
	}
	if cfg.Chats["default"].System != "general" {
		t.Errorf("System = %q, want %q", cfg.Chats["default"].System, "general")
	}
	if cfg.SystemPrompts["general"] != "You are a helpful assistant." {
		t.Errorf("SystemPrompts[general] = %q", cfg.SystemPrompts["general"])
	}
}

func TestLoadConfigMixedCase(t *testing.T) {
	// Mix camelCase and snake_case in the same config
	tmp := t.TempDir()
	path := tmp + "/config.yml"
	data := `
providers:
  openai:
    type: openai
    base_url: https://api.openai.com
    apiKey: sk-camel
models:
  gpt4:
    provider: openai
    model: gpt-4
    max_tokens: 4096
    reasoningEffort: low
chats:
  default:
    model: gpt4
    system: general
    max_message_rounds: 5
    maxRetries: 2
mcp_servers:
  myserver:
    type: sse
    url: http://localhost:8080
    no_concurrent: true
    noConcurrentTools:
      - search
    lowercase_tools: true
system_prompts:
  general: "You are a helpful assistant."
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig with mixed case failed: %v", err)
	}

	if _, ok := cfg.SystemPrompts["general"]; !ok {
		t.Error("system_prompts.general not parsed")
	}

	if cfg.Providers["openai"].BaseURL != "https://api.openai.com" {
		t.Errorf("BaseURL = %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Providers["openai"].APIKey != "sk-camel" {
		t.Errorf("APIKey = %q", cfg.Providers["openai"].APIKey)
	}
	if cfg.Models["gpt4"].MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", cfg.Models["gpt4"].MaxTokens)
	}
	if cfg.Models["gpt4"].ReasoningEffort == nil || *cfg.Models["gpt4"].ReasoningEffort != "low" {
		t.Errorf("ReasoningEffort = %v", cfg.Models["gpt4"].ReasoningEffort)
	}
	if cfg.Chats["default"].MaxMessageRounds != 5 {
		t.Errorf("MaxMessageRounds = %d", cfg.Chats["default"].MaxMessageRounds)
	}
	if cfg.Chats["default"].MaxRetries != 2 {
		t.Errorf("MaxRetries = %d", cfg.Chats["default"].MaxRetries)
	}
	if !cfg.MCPServers["myserver"].NoConcurrent {
		t.Errorf("NoConcurrent = %v", cfg.MCPServers["myserver"].NoConcurrent)
	}
	if len(cfg.MCPServers["myserver"].NoConcurrentTools) != 1 ||
		cfg.MCPServers["myserver"].NoConcurrentTools[0] != "search" {
		t.Errorf("NoConcurrentTools = %v", cfg.MCPServers["myserver"].NoConcurrentTools)
	}
	if !cfg.MCPServers["myserver"].LowercaseTools {
		t.Errorf("LowercaseTools = %v", cfg.MCPServers["myserver"].LowercaseTools)
	}
	if cfg.Chats["default"].System != "general" {
		t.Errorf("System = %q, want %q", cfg.Chats["default"].System, "general")
	}
	if cfg.SystemPrompts["general"] != "You are a helpful assistant." {
		t.Errorf("SystemPrompts[general] = %q", cfg.SystemPrompts["general"])
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestGetConfig(t *testing.T) {
	// Save and restore global config to avoid test ordering issues
	prev := globalConfig
	globalConfig = nil
	defer func() { globalConfig = prev }()

	if GetConfig() != nil {
		t.Error("expected nil when globalConfig is not set")
	}

	// Set it and verify we can get it back
	testCfg := &Config{}
	globalConfig = testCfg
	if GetConfig() != testCfg {
		t.Error("expected same config pointer")
	}
}

func TestExtraBodyKeysPreserved(t *testing.T) {
	// Verify that keys inside extraBody are NOT converted from snake_case to camelCase.
	tmp := t.TempDir()
	path := tmp + "/config.yml"
	data := `
models:
  gpt4:
    provider: openai
    model: gpt-4
    extra_body:
      some_snake_key: value1
      another_key: value2
      camelKey: value3
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	extraBody := cfg.Models["gpt4"].ExtraBody
	if extraBody == nil {
		t.Fatal("ExtraBody should not be nil")
	}

	// Keys should be preserved as-is, not converted
	if _, ok := extraBody["some_snake_key"]; !ok {
		t.Error("some_snake_key should be preserved")
	}
	if _, ok := extraBody["someSnakeKey"]; ok {
		t.Error("someSnakeKey should NOT exist — key was incorrectly converted")
	}
	if _, ok := extraBody["another_key"]; !ok {
		t.Error("another_key should be preserved")
	}
	if _, ok := extraBody["anotherKey"]; ok {
		t.Error("anotherKey should NOT exist — key was incorrectly converted")
	}
	if _, ok := extraBody["camelKey"]; !ok {
		t.Error("camelKey should be preserved")
	}
}

func TestExtraBodyMixedCase(t *testing.T) {
	// Verify extra_body (snake_case yaml key) also works and its inner keys are preserved.
	tmp := t.TempDir()
	path := tmp + "/config.yml"
	data := `
models:
  gpt4:
    provider: openai
    model: gpt-4
    extra_body:
      nested_snake: hello
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	extraBody := cfg.Models["gpt4"].ExtraBody
	if extraBody == nil {
		t.Fatal("ExtraBody should not be nil")
	}
	if v, ok := extraBody["nested_snake"]; !ok || v != "hello" {
		t.Errorf("nested_snake should be 'hello', got %v", v)
	}
	if _, ok := extraBody["nestedSnake"]; ok {
		t.Error("nestedSnake should NOT exist")
	}
}

func TestResolveSystemPrompt(t *testing.T) {
	cfg := &Config{
		SystemPrompts: map[string]string{
			"general": "You are a helpful assistant.",
			"coder":   "You are an expert programmer.",
		},
	}

	tests := []struct {
		name     string
		prompt   string
		expected string
		wantErr  bool
	}{
		{
			name:     "literal prompt",
			prompt:   "You are a custom assistant.",
			expected: "You are a custom assistant.",
		},
		{
			name:     "reference to systemPrompts key",
			prompt:   "general",
			expected: "You are a helpful assistant.",
		},
		{
			name:     "reference to another key",
			prompt:   "coder",
			expected: "You are an expert programmer.",
		},
		{
			name:     "empty prompt",
			prompt:   "",
			expected: "",
		},
		{
			name:    "file reference - nonexistent",
			prompt:  "@file:/nonexistent/path.txt",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSystemPrompt(cfg, tt.prompt)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("ResolveSystemPrompt() = %q, want %q", got, tt.expected)
			}
		})
	}

	// Test @file: with a real file
	tmp := t.TempDir()
	filePath := tmp + "/prompt.txt"
	content := "Hello from file!"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveSystemPrompt(cfg, "@file:"+filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != content {
		t.Errorf("ResolveSystemPrompt(@file:) = %q, want %q", got, content)
	}
}
