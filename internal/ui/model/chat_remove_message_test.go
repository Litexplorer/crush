package model

import (
	"testing"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/ui/chat"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/stretchr/testify/require"
)

// TestChat_RemoveMessageRemovesAliasedItems is a regression test for the
// delete-turn flow: a single assistant message renders as several items
// (the content item, per-tool-call entries, and the end-of-turn info
// footer) that all alias the same message ID. RemoveMessage must drop
// every item owned by the message, not just the last-registered one.
func TestChat_RemoveMessageRemovesAliasedItems(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u1", messageID: "u1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "a1", messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a", messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: chat.AssistantInfoID("a1"), messageID: "a1"},
	}
	c.SetMessages(items...)
	c.SetSize(100, 10)
	require.Equal(t, 4, c.Len())

	// DeletedEvent for the assistant message, then the (already removed)
	// tool row and info footer events.
	c.RemoveMessage("a1")
	c.RemoveMessage("tc-a")
	c.RemoveMessage(chat.AssistantInfoID("a1"))

	require.Equal(t, 1, c.Len())
	require.Nil(t, c.MessageItem("a1"))
	require.Nil(t, c.MessageItem("tc-a"))
	require.Nil(t, c.MessageItem(chat.AssistantInfoID("a1")))
	require.NotNil(t, c.MessageItem("u1"))
}

// TestChat_RemoveMessageKeepsOtherTurns verifies that removing the items
// of one turn never touches items owned by other turns, including tool
// items that alias a different assistant message ID.
func TestChat_RemoveMessageKeepsOtherTurns(t *testing.T) {
	com := common.DefaultCommon(nil)
	c := NewChat(com, config.ScrollbarDefault)

	items := []chat.MessageItem{
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u1", messageID: "u1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "a1", messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "tc-a1", messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: chat.AssistantInfoID("a1"), messageID: "a1"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "u2", messageID: "u2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: "a2", messageID: "a2"},
		&fakeChatItem{Versioned: list.NewVersioned(), id: chat.AssistantInfoID("a2"), messageID: "a2"},
	}
	c.SetMessages(items...)
	c.SetSize(100, 10)
	require.Equal(t, 7, c.Len())

	c.RemoveMessage("u2")
	c.RemoveMessage("a2")
	c.RemoveMessage(chat.AssistantInfoID("a2"))

	require.Equal(t, 4, c.Len())
	require.NotNil(t, c.MessageItem("u1"))
	require.NotNil(t, c.MessageItem("a1"))
	require.NotNil(t, c.MessageItem("tc-a1"))
	require.NotNil(t, c.MessageItem(chat.AssistantInfoID("a1")))
	require.Nil(t, c.MessageItem("u2"))
	require.Nil(t, c.MessageItem("a2"))
}