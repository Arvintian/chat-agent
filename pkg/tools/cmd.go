package tools

import (
	"context"
	"encoding/json"
	"time"

	skilltools "github.com/Arvintian/chat-agent/pkg/skills/tools"
	"github.com/Arvintian/chat-agent/pkg/utils"

	"github.com/cloudwego/eino/components/tool"
)

type cmdConfig struct {
	WorkDir string `json:"workDir"`
	Timeout int    `json:"timeout"`
}

func getCommandTools(ctx context.Context, params map[string]interface{}) ([]tool.BaseTool, error) {
	var cfg cmdConfig
	bts, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(bts), &cfg); err != nil {
		return nil, err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30
	}
	var bash *utils.BashManager
	if v, ok := ctx.Value("bash").(*utils.BashManager); ok {
		bash = v
	}
	cmdTool := skilltools.RunTerminalCommandTool{
		Name:        "cmd",
		WorkingDir:  cfg.WorkDir,
		Timeout:     time.Duration(cfg.Timeout) * time.Second,
		BashMannger: bash,
	}
	return []tool.BaseTool{&cmdTool}, nil
}
