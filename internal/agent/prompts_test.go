package agent

import (
	"context"
	"testing"

	"github.com/charmbracelet/crush/internal/agent/prompt"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/stretchr/testify/require"
)

// TestTaskPromptIncludesSkills guards the sub-agent system prompt against
// losing its skill catalog. The skill XML was only rendered by the coder
// template, so sub-agents spawned through the `agent` tool could not discover
// or load skills even though their view tool already understood
// crush://skills/ locations.
func TestTaskPromptIncludesSkills(t *testing.T) {
	workingDir := t.TempDir()
	cfg, err := config.Init(workingDir, "", false)
	require.NoError(t, err)
	// Keep the assertion deterministic regardless of the host's user config.
	cfg.Config().Options.SkillsPaths = nil
	cfg.Config().Options.DisabledSkills = nil

	p, err := taskPrompt(prompt.WithWorkingDir(workingDir))
	require.NoError(t, err)

	built, err := p.Build(context.Background(), "", "", cfg)
	require.NoError(t, err)

	require.Contains(t, built, "<available_skills>")
	require.Contains(t, built, "crush://skills/")
	require.Contains(t, built, "</skills_usage>")
}
