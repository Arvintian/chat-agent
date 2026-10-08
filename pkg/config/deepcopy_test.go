package config

import (
	"testing"
)

// A config with every nested field populated (maps, slices, pointers,
// map[string]any) for the DeepCopy tests.
func newRichConfig() *Config {
	return &Config{
		Chats: map[string]Chat{
			"chat1": {
				Desc:        "desc",
				System:      "system",
				Model:       "model1",
				Persistence: true,
				Tools:       []string{"tool1"},
				Hooks: &SessionHooks{
					Keep: &SessionHookConfig{
						Enabled: true,
						Type:    "script",
						Env:     map[string]string{"K": "V"},
					},
				},
				Cron: []CronTask{
					{
						Expr:   "* * * * *",
						Prompt: "p",
						Hook:   &SessionHookConfig{Enabled: true, Env: map[string]string{"A": "B"}},
					},
				},
			},
		},
		Providers: map[string]Provider{
			"p1": {Type: "openai", Headers: map[string]string{"H": "V"}},
		},
		Models: map[string]Model{
			"m1": {ModelParams: ModelParams{
				Provider: "p1",
				Model:    "model1",
				ExtraBody: map[string]any{
					"nested": map[string]any{"k": "v"},
					"num":    42,
				},
			}},
		},
		MCPServers: map[string]MCPServer{
			"srv1": {Type: "sse", Include: []string{"t1"}, NoConcurrent: true},
		},
		Tools: map[string]Tool{
			"t1": {
				Category: "cmd",
				Params: map[string]any{
					"workDir": "/tmp",
					"env":     map[string]string{"X": "Y"},
				},
			},
		},
		SystemPrompts: map[string]string{"sp1": "text"},
	}
}

func TestConfigDeepCopyFidelity(t *testing.T) {
	original := newRichConfig()
	cp, err := original.DeepCopy()
	if err != nil {
		t.Fatalf("DeepCopy failed: %v", err)
	}
	if cp.Chats["chat1"].Cron[0].Hook.Env["A"] != "B" {
		t.Fatalf("cron hook env not copied: %v", cp.Chats["chat1"].Cron[0].Hook.Env)
	}
	if cp.Models["m1"].ExtraBody["nested"].(map[string]any)["k"] != "v" {
		t.Fatalf("nested extraBody not copied: %v", cp.Models["m1"].ExtraBody)
	}
	if cp.Tools["t1"].Params["workDir"] != "/tmp" {
		t.Fatalf("tool params not copied: %v", cp.Tools["t1"].Params)
	}
	if cp.MCPServers["srv1"].Include[0] != "t1" {
		t.Fatalf("mcp include not copied: %v", cp.MCPServers["srv1"].Include)
	}
}

// Mutations on the copy (top-level fields and every nested level) must not
// leak back into the original config.
func TestConfigDeepCopyIndependence(t *testing.T) {
	original := newRichConfig()
	cp, err := original.DeepCopy()
	if err != nil {
		t.Fatalf("DeepCopy failed: %v", err)
	}

	// mutate every level of the copy
	chat1 := cp.Chats["chat1"]
	chat1.Persistence = false
	chat1.Tools = append(chat1.Tools, "tool2")
	chat1.Cron[0].Hook.Env["A"] = "MUTATED"
	chat1.Hooks.Keep.Env["K"] = "MUTATED"
	cp.Chats["chat1"] = chat1
	cp.Providers["p1"].Headers["H"] = "MUTATED"
	cp.Models["m1"].ExtraBody["nested"].(map[string]any)["k"] = "MUTATED"
	srv1 := cp.MCPServers["srv1"]
	srv1.Include = append(srv1.Include, "t2")
	cp.MCPServers["srv1"] = srv1
	cp.Tools["t1"].Params["env"].(map[string]any)["X"] = "MUTATED"
	cp.SystemPrompts["sp1"] = "MUTATED"

	// the original must be untouched
	if original.Chats["chat1"].Persistence != true {
		t.Error("chat persistence leaked from copy to original")
	}
	if len(original.Chats["chat1"].Tools) != 1 {
		t.Error("chat tools slice leaked from copy to original")
	}
	if original.Chats["chat1"].Cron[0].Hook.Env["A"] != "B" {
		t.Error("cron hook env leaked from copy to original")
	}
	if original.Chats["chat1"].Hooks.Keep.Env["K"] != "V" {
		t.Error("session hook env leaked from copy to original")
	}
	if original.Providers["p1"].Headers["H"] != "V" {
		t.Error("provider headers leaked from copy to original")
	}
	if original.Models["m1"].ExtraBody["nested"].(map[string]any)["k"] != "v" {
		t.Error("nested extraBody leaked from copy to original")
	}
	if len(original.MCPServers["srv1"].Include) != 1 {
		t.Error("mcp include slice leaked from copy to original")
	}
	if original.Tools["t1"].Params["env"].(map[string]string)["X"] != "Y" {
		t.Error("tool params leaked from copy to original")
	}
	if original.SystemPrompts["sp1"] != "text" {
		t.Error("system prompts leaked from copy to original")
	}
}
