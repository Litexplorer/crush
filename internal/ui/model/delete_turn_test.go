package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

// TestChat_SelectedMessageID verifies the selection-to-message mapping
// used to anchor delete-turn operations.
func TestChat_SelectedMessageID(t *testing.T) {
	t.Parallel()
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m1", messageID: "m1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "m2", messageID: "m2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a", messageID: "a1"},
	}
	c.SetMessages(items...)

	// No selection yet.
	require.Equal(t, "", c.SelectedMessageID())

	c.list.SetSelected(0)
	require.Equal(t, "m1", c.SelectedMessageID())

	// A tool-only item maps to its owning message ID.
	c.list.SetSelected(2)
	require.Equal(t, "a1", c.SelectedMessageID())
}

// TestChat_MessageIDProviderContract ensures the built-in item types used
// in real sessions implement MessageIDProvider via the chat package.
func TestChat_MessageIDProviderContract(t *testing.T) {
	t.Parallel()
	require.Implements(t, (*chat.MessageIDProvider)(nil), (*chat.AssistantMessageItem)(nil))
	require.Implements(t, (*chat.MessageIDProvider)(nil), (*chat.UserMessageItem)(nil))
	require.Implements(t, (*chat.MessageIDProvider)(nil), (*chat.AssistantInfoItem)(nil))
	require.Implements(t, (*chat.MessageIDProvider)(nil), (*chat.ShellItem)(nil))
}
