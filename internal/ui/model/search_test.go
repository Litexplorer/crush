package model

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// TestOpenSearchDialogOpensAndDoesNotStack verifies the search dialog
// opens once and that a second open brings it to the front instead of
// stacking another copy.
func TestOpenSearchDialogOpensAndDoesNotStack(t *testing.T) {
	u := newTestUIWithConfig(t, nil)
	u.com = common.DefaultCommon(u.com.Workspace)
	u.header = newHeader(u.com)
	u.dialog = dialog.NewOverlay()

	u.openSearchDialog()
	require.True(t, u.dialog.ContainsDialog(dialog.SearchID), "search dialog should open")

	u.openSearchDialog()
	require.True(t, u.dialog.ContainsDialog(dialog.SearchID))
	require.NotNil(t, u.dialog.DialogLast(), "search dialog should be the front dialog")
}

// TestHeaderSearchButtonClickOpensSearchDialog verifies that a left
// click on the header search button opens the search dialog.
func TestHeaderSearchButtonClickOpensSearchDialog(t *testing.T) {
	u := newTestUIWithConfig(t, nil)
	u.com = common.DefaultCommon(u.com.Workspace)
	u.header = newHeader(u.com)
	u.dialog = dialog.NewOverlay()

	u.state = uiChat
	u.focus = uiFocusMain
	u.header.searchBtnWidth = 8
	u.layout.header = image.Rect(0, 0, 100, 1)

	_ = u.handleClickFocus(tea.MouseClickMsg(tea.Mouse{
		X:      95, // within the right-side button region
		Y:      0,
		Button: uv.MouseLeft,
	}))
	require.True(t, u.dialog.ContainsDialog(dialog.SearchID), "header search button click should open the search dialog")
}

// TestHeaderSearchButtonClickOutsideRegionDoesNotOpen verifies clicks
// on the left side of the header do not open the search dialog.
func TestHeaderSearchButtonClickOutsideRegionDoesNotOpen(t *testing.T) {
	u := newTestUIWithConfig(t, nil)
	u.com = common.DefaultCommon(u.com.Workspace)
	u.header = newHeader(u.com)
	u.dialog = dialog.NewOverlay()

	u.state = uiChat
	u.focus = uiFocusMain
	u.header.searchBtnWidth = 8
	u.layout.header = image.Rect(0, 0, 100, 1)

	cmd := u.handleClickFocus(tea.MouseClickMsg(tea.Mouse{
		X:      10, // left side, not the button
		Y:      0,
		Button: uv.MouseLeft,
	}))
	require.False(t, u.dialog.ContainsDialog(dialog.SearchID))
	require.Nil(t, cmd)
}
