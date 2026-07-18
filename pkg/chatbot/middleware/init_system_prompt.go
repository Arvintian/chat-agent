package middleware

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type PromptRenderer func(prompt string) (string, error)

// InitSystemPrompt swaps the system prompt to initSystemPrompt on the very first
// model call (when state has exactly [system, user]). After the call, it restores
// the normal prompt. Subsequent calls in the same ReAct loop or later rounds are
// no-ops. After Clear() resets state, the init prompt fires again automatically.
type InitSystemPrompt struct {
	*adk.BaseChatModelAgentMiddleware
	initPrompt string
	normal     string
	renderer   PromptRenderer
	swapped    bool
}

func NewInitSystemPrompt(initPrompt, normal string, renderer PromptRenderer) *InitSystemPrompt {
	return &InitSystemPrompt{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		initPrompt:                   initPrompt,
		normal:                       normal,
		renderer:                     renderer,
	}
}

func (m *InitSystemPrompt) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m.initPrompt == "" || m.swapped || len(state.Messages) != 2 {
		return ctx, state, nil
	}

	for i, msg := range state.Messages {
		if msg.Role == schema.System {
			rendered, err := m.renderer(m.initPrompt)
			if err != nil {
				return ctx, state, err
			}
			state.Messages[i] = schema.SystemMessage(rendered)
			m.swapped = true
			break
		}
	}
	return ctx, state, nil
}

func (m *InitSystemPrompt) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if !m.swapped {
		return ctx, state, nil
	}

	for i, msg := range state.Messages {
		if msg.Role == schema.System {
			rendered, err := m.renderer(m.normal)
			if err != nil {
				return ctx, state, err
			}
			state.Messages[i] = schema.SystemMessage(rendered)
			break
		}
	}
	m.swapped = false
	return ctx, state, nil
}
