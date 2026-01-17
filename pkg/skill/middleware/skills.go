// Package middleware provides eino middleware for skills integration.
package middleware

import (
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/Arvintian/chat-agent/pkg/skill/loader"
	"github.com/Arvintian/chat-agent/pkg/skill/tools"
)

// SkillsMiddleware injects skills metadata into agent prompts
// and provides skill-related tools.
type SkillsMiddleware struct {
	registry *loader.Registry
	tools    []tool.BaseTool
}

// NewSkillsMiddleware creates a new skills middleware.
func NewSkillsMiddleware(registry *loader.Registry) *SkillsMiddleware {
	mw := &SkillsMiddleware{
		registry: registry,
		tools:    tools.NewSkillTools(registry),
	}

	return mw
}

// InjectPrompt adds skills information to the system prompt.
func (m *SkillsMiddleware) InjectPrompt(basePrompt string) string {
	skillsSection := m.registry.GenerateSystemPromptSection()
	instructions := m.registry.GenerateSkillsInstructions()

	if skillsSection == "" {
		return basePrompt
	}

	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n")
	sb.WriteString(skillsSection)
	sb.WriteString("\n")
	sb.WriteString(instructions)

	return sb.String()
}

// GetTools returns skill-related tools to add to the agent.
func (m *SkillsMiddleware) GetTools() []tool.BaseTool {
	return m.tools
}
