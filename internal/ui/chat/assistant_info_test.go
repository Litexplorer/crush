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

// TestAssistantInfoItemTurnRate covers the end-of-turn footer: the rate is the
// turn accounting persisted on the finish part, so the same numbers survive a
// restart, and it is simply absent when no accounting was recorded (messages
// written before the metrics existed, error and cancel finishes).
func TestAssistantInfoItemTurnRate(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	start := time.Unix(1_700_000_000, 0)
	msg := &message.Message{
		ID:   "info",
		Role: message.Assistant,
		Parts: []message.ContentPart{message.Finish{
			Reason:       message.FinishReasonEndTurn,
			Time:         start.Add(20 * time.Second).Unix(),
			OutputTokens: 400,
			GenerationMS: 20_000,
		}},
	}

	item := NewAssistantInfoItem(&sty, msg, cfg, start).(*AssistantInfoItem)
	rendered := ansi.Strip(item.RawRender(80))
	require.Contains(t, rendered, "in 20s")
	require.Contains(t, rendered, "20 tok/s", "400 tokens over 20s of generation")

	// A turn that spent most of its wall clock in tools reports only the
	// generation time, not the whole turn.
	toolHeavy := &message.Message{
		ID:   "tools",
		Role: message.Assistant,
		Parts: []message.ContentPart{message.Finish{
			Reason:       message.FinishReasonEndTurn,
			Time:         start.Add(833 * time.Second).Unix(),
			OutputTokens: 12_000,
			GenerationMS: 60_000,
		}},
	}
	toolHeavyItem := NewAssistantInfoItem(&sty, toolHeavy, cfg, start).(*AssistantInfoItem)
	require.Contains(t, ansi.Strip(toolHeavyItem.RawRender(80)), "in 13m53s")
	require.Contains(t, ansi.Strip(toolHeavyItem.RawRender(80)), "200 tok/s")

	historical := &message.Message{
		ID:   "historical",
		Role: message.Assistant,
		Parts: []message.ContentPart{message.Finish{
			Reason: message.FinishReasonEndTurn,
			Time:   start.Add(20 * time.Second).Unix(),
		}},
	}
	require.NotContains(t, ansi.Strip(NewAssistantInfoItem(&sty, historical, cfg, start).RawRender(80)), "tok/s",
		"messages without turn accounting must not show a rate")
}
