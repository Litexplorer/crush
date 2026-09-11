package chat

import (
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestAssistantInfoItemTurnRate covers the end-of-turn footer: the rate is
// the turn's output tokens divided by the same duration the footer already
// prints next to it, and it is simply absent when no turn accounting was
// supplied (restored sessions).
func TestAssistantInfoItemTurnRate(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	start := time.Unix(1_700_000_000, 0)
	msg := &message.Message{
		ID:   "info",
		Role: message.Assistant,
		Parts: []message.ContentPart{message.Finish{
			Reason: message.FinishReasonEndTurn,
			Time:   start.Add(20 * time.Second).Unix(),
		}},
	}

	item := NewAssistantInfoItem(&sty, msg, cfg, start, 400).(*AssistantInfoItem)
	rendered := ansi.Strip(item.RawRender(80))
	require.Contains(t, rendered, "in 20s")
	require.Contains(t, rendered, "20 tok/s", "400 tokens over 20s")

	historical := NewAssistantInfoItem(&sty, msg, cfg, start, 0).(*AssistantInfoItem)
	require.NotContains(t, ansi.Strip(historical.RawRender(80)), "tok/s",
		"messages restored from disk must not show a rate")
}
