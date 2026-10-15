package model

import (
	"context"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/agent/tools/mcp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/skills"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/completions"
	"github.com/stretchr/testify/require"
)

// ReadSkill backs the `#` skill completion: it hands the popup a fixed body so
// the test can assert the skill content reaches the attachment.
func (w *testWorkspace) ReadSkill(context.Context, string) ([]byte, skills.SkillReadResult, error) {
	return []byte("# Dev\n\nDo the thing."), skills.SkillReadResult{Name: "dev"}, nil
}

// ListSkills backs loadMentionItems.
func (w *testWorkspace) ListSkills(context.Context) ([]skills.CatalogEntry, error) {
	return []skills.CatalogEntry{
		{ID: "skills/dev/SKILL.md", Name: "dev", Description: "Development workflow"},
	}, nil
}

// MCPGetStates backs loadMentionItems, offering one live and one disabled
// server so the disabled one can be filtered out.
func (w *testWorkspace) MCPGetStates() map[string]mcp.ClientInfo {
	return map[string]mcp.ClientInfo{
		"connected": {Name: "connected", State: mcp.StateConnected},
		"disabled":  {Name: "disabled", State: mcp.StateDisabled},
	}
}

// batchMsgs runs a command and returns the messages it produces. tea.Batch
// collapses to the single command when only one survives, so both shapes are
// handled here.
func batchMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}

	msgs := make([]tea.Msg, 0, len(batch))
	for _, child := range batch {
		msgs = append(msgs, child())
	}
	return msgs
}

func TestInsertMCPCompletionReplacesTriggerWord(t *testing.T) {
	t.Parallel()

	u := &UI{textarea: textarea.New()}
	u.textarea.SetValue("run this #git")
	u.completionsStartIndex = len("run this ")

	u.insertMCPCompletion(completions.MCPCompletionValue{Name: "github"})

	require.Equal(t, `run this #github (use the "github" MCP server tools) `, u.textarea.Value())
}

func TestInsertSkillCompletionAttachesSkillContent(t *testing.T) {
	t.Parallel()

	u := &UI{
		com:      &common.Common{Workspace: &testWorkspace{}},
		textarea: textarea.New(),
	}
	u.textarea.SetValue("#dev")
	u.completionsStartIndex = 0

	msgs := batchMsgs(t, u.insertSkillCompletion(completions.SkillCompletionValue{
		ID:   "skills/dev/SKILL.md",
		Name: "dev",
	}))

	// The prompt keeps a visible reference to the skill.
	require.Equal(t, "#dev ", u.textarea.Value())

	require.Len(t, msgs, 1)
	attachment, ok := msgs[0].(message.Attachment)
	require.True(t, ok)
	require.Equal(t, "dev", attachment.FileName)
	require.Equal(t, "text/markdown", attachment.MimeType)
	require.Equal(t, "# Dev\n\nDo the thing.", string(attachment.Content))
}

func TestLoadMentionItemsSkipsDisabledServers(t *testing.T) {
	t.Parallel()

	u := &UI{com: &common.Common{Workspace: &testWorkspace{}}}

	skillItems, serverItems := u.loadMentionItems()

	require.Len(t, skillItems, 1)
	require.Equal(t, "dev", skillItems[0].Name)
	require.Equal(t, "skills/dev/SKILL.md", skillItems[0].ID)

	require.Len(t, serverItems, 1)
	require.Equal(t, "connected", serverItems[0].Name)
}
