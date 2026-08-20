package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/hooks"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/stretchr/testify/require"
)

// hookMetadataJSON builds a metadata JSON string with the given hook infos,
// mirroring what mergeHookMetadata produces in the agent package.
func hookMetadataJSONForTest(infos ...hooks.HookInfo) string {
	meta := hooks.HookMetadata{
		HookCount: len(infos),
		Hooks:     infos,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return `{"hook":` + string(data) + `}`
}

func TestToolOutputHookIndicator(t *testing.T) {
	t.Parallel()
	sty := common.DefaultCommon(nil).Styles

	t.Run("empty metadata renders nothing", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "", toolOutputHookIndicator(sty, "", 80))
		require.Equal(t, "", toolOutputHookIndicator(sty, `{"other":1}`, 80))
	})

	t.Run("single post hook renders one line", func(t *testing.T) {
		t.Parallel()
		md := hookMetadataJSONForTest(hooks.HookInfo{Name: "view-chars"})
		out := toolOutputHookIndicator(sty, md, 80)
		require.NotEmpty(t, out)
		require.Contains(t, out, "view-chars")
		require.Contains(t, out, "Hook")
	})

	t.Run("pre and post hooks render two lines", func(t *testing.T) {
		t.Parallel()
		md := hookMetadataJSONForTest(
			hooks.HookInfo{Name: "rtk-hook"},
			hooks.HookInfo{Name: "view-chars"},
		)
		out := toolOutputHookIndicator(sty, md, 80)
		require.NotEmpty(t, out)
		require.Contains(t, out, "rtk-hook")
		require.Contains(t, out, "view-chars")
		// Two distinct hook lines.
		require.Equal(t, 2, strings.Count(out, "Hook"))
	})

	t.Run("matcher and decision shown", func(t *testing.T) {
		t.Parallel()
		md := hookMetadataJSONForTest(hooks.HookInfo{
			Name:    "vet",
			Matcher: "^write$",
		})
		out := toolOutputHookIndicator(sty, md, 80)
		require.Contains(t, out, "vet")
		require.Contains(t, out, "^write$")
	})
}
