package loader

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Registry manages loaded skills and provides lookup functionality.
type Registry struct {
	mu       sync.RWMutex
	skills   map[string]*Skill
	metadata []SkillMetadata
	loader   *Loader
}

// RegistryOption configures the Registry.
type RegistryOption func(*Registry)

// NewRegistry creates a new skills registry.
func NewRegistry(loader *Loader, opts ...RegistryOption) *Registry {
	r := &Registry{
		skills: make(map[string]*Skill),
		loader: loader,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Initialize loads all skills from configured directories.
func (r *Registry) Initialize(ctx context.Context) error {
	r.mu.Lock()

	// Load metadata for system prompt
	metadata, err := r.loader.LoadMetadataOnly(ctx)
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("failed to load skill metadata: %w", err)
	}
	r.metadata = metadata

	// Clear existing skills
	r.skills = make(map[string]*Skill)

	r.mu.Unlock()

	return nil
}

// Get retrieves a skill by name, loading it on demand if needed.
func (r *Registry) Get(ctx context.Context, name string) (*Skill, error) {
	r.mu.RLock()
	skill, exists := r.skills[name]
	r.mu.RUnlock()

	if exists {
		return skill, nil
	}

	// Load on demand
	skill, err := r.loader.LoadSkill(ctx, name)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.skills[name] = skill
	r.mu.Unlock()

	return skill, nil
}

// GetContent retrieves the full content of a skill.
func (r *Registry) GetContent(ctx context.Context, name string) (string, error) {
	skill, err := r.Get(ctx, name)
	if err != nil {
		return "", err
	}

	return r.loader.LoadSkillContent(ctx, skill)
}

// GetMetadata returns all loaded skill metadata.
func (r *Registry) GetMetadata() []SkillMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metadata
}

// FindMatchingSkill finds a skill that matches the given query.
// This uses simple keyword matching for skill selection.
func (r *Registry) FindMatchingSkill(query string) *SkillMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var bestMatch *SkillMetadata
	bestScore := 0

	for i := range r.metadata {
		m := &r.metadata[i]
		score := r.calculateMatchScore(query, m)
		if score > bestScore {
			bestScore = score
			bestMatch = m
		}
	}

	// Require minimum score to return a match
	if bestScore < 2 {
		return nil
	}

	return bestMatch
}

// calculateMatchScore computes how well a skill matches a query.
func (r *Registry) calculateMatchScore(query string, m *SkillMetadata) int {
	score := 0
	queryWords := strings.Fields(query)

	// Check skill name
	name := strings.ToLower(m.Name)
	for _, word := range queryWords {
		if strings.Contains(name, word) {
			score += 3
		}
	}

	// Check description
	desc := strings.ToLower(m.Description)
	for _, word := range queryWords {
		if len(word) > 2 && strings.Contains(desc, word) {
			score += 1
		}
	}

	return score
}

// GenerateSystemPromptSection generates the skills section for system prompts.
func (r *Registry) GenerateSystemPromptSection() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.metadata) == 0 {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(`
You can accomplish things through specialized skills.

**CRITICAL INSTRUCTIONS FOR SKILL EXECUTION:**

1. **DISCOVERY & LOADING (Token-Efficient)**:
   - Use 'list_skills' to find relevant skills
   - **ALWAYS use view_skill with toc=true FIRST** to see the structure (saves 80-90% tokens)
   - Then use section parameter to load only the parts you need
   - Example: view_skill(name='git-commit', toc=true) → see all sections → view_skill(name='git-commit', section='Instructions')
   - Only load full content if absolutely necessary
2. **STRICT STEP-BY-STEP EXECUTION**:
   - You MUST follow the loaded skill's workflow exactly as written.
   - **DO NOT SKIP STEPS**: If the skill defines an "Analysis", "Preparation", or "Check" phase, you MUST execute it before moving to the main action.
3. **EXECUTE WITH ROBUSTNESS**:
   - **USE ABSOLUTE PATHS**: Construct **ABSOLUTE PATHS** for scripts referenced in the skill (e.g., '~/.claude/skills/[skill-name]/scripts/[script-name]').
   - **EXECUTE DIRECTLY**: Run the command directly. **DO NOT** use 'ls' or 'stat' to check existence first.
4. **ERROR RECOVERY (Fix & Retry)**:
   - If a command fails (e.g., "No such file"), **ANALYZE the error**.
   - **Retry with Fix**:
     - Did you use a relative path? Retry with the absolute path.
     - Are you in the wrong directory? Retry with correct context or path.
   - **Fallback**: Only if the script is truly broken or missing after retries, fallback to using **equivalent native commands** to accomplish the step's goal.
5. **TRANSPARENCY**: Explicitly state your thinking process (e.g., "Step 1: Running analysis script...").

`)

	sb.WriteString("<available_skills>\n")

	for _, m := range r.metadata {
		sb.WriteString("<skill>\n")
		sb.WriteString(fmt.Sprintf("<name>\n%s\n</name>\n", m.Name))
		sb.WriteString(fmt.Sprintf("<description>\n%s\n</description>\n", m.Description))
		sb.WriteString(fmt.Sprintf("<location>\n%s/SKILL.md\n</location>\n", m.Path))
		sb.WriteString("</skill>\n\n")
	}

	sb.WriteString("</available_skills>\n")

	return sb.String()
}

// GenerateSkillsInstructions generates instructions for using skills.
func (r *Registry) GenerateSkillsInstructions() string {
	return `<skills_instructions>
When a task matches one of the available skills, follow these steps:

1. **Discovery**: Check <available_skills> to see if any skill matches the current task
2. **Load Instructions**: Use the view tool or read_file to load the full SKILL.md content from the skill's location
3. **Follow Instructions**: Execute the task according to the loaded skill instructions
4. **Reference Files**: If the skill references additional files (scripts/, references/, assets/), load them as needed

Skills provide specialized workflows and domain knowledge. Always prefer using a relevant skill over improvising when one is available.
</skills_instructions>
`
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.metadata)
}

// Names returns all registered skill names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.metadata))
	for i, m := range r.metadata {
		names[i] = m.Name
	}
	return names
}
