package model

import (
	"reflect"
	"slices"
	"testing"

	"charm.land/x/nerdfont"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// TestLSPDiagnosticsIcons covers the LSP diagnostic counts shown in the
// sidebar: the Nerd Font severity glyphs render only when the terminal is
// expected to render them, and the E, W, I, and H letters remain the
// fallback otherwise.
func TestLSPDiagnosticsIcons(t *testing.T) {
	// Not parallel: t.Setenv is process-wide, as is the nerdfont probe memo.
	st := styles.CharmtonePantera()
	diagnostics := map[protocol.DiagnosticSeverity]int{
		protocol.SeverityError:       1,
		protocol.SeverityWarning:     2,
		protocol.SeverityInformation: 3,
		protocol.SeverityHint:        4,
	}

	tests := []struct {
		name      string
		nerdFonts string
		want      string
	}{
		{
			name:      "glyphs with nerd font support",
			nerdFonts: "true",
			want: styles.LSPErrorIcon + "1 " +
				styles.LSPWarningIcon + "2 " +
				styles.LSPHintIcon + "4 " +
				styles.LSPInfoIcon + "3",
		},
		{
			name:      "letters without nerd font support",
			nerdFonts: "false",
			want:      "E1 W2 H4 I3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(nerdfont.EnvVar, tt.nerdFonts)
			nerdfont.Reset()
			t.Cleanup(nerdfont.Reset)

			require.Equal(t, tt.want, ansi.Strip(lspDiagnostics(&st, diagnostics)))
		})
	}
}

func TestSortLSPsByActivation(t *testing.T) {
	t.Parallel()

	mk := func(name string, state lsp.ServerState) workspace.LSPClientInfo {
		return workspace.LSPClientInfo{Name: name, State: state}
	}

	tests := []struct {
		name string
		in   []workspace.LSPClientInfo
		want []string
	}{
		{
			name: "active states (Ready, Starting) sort before inactive",
			in: []workspace.LSPClientInfo{
				mk("zebra", lsp.StateUnstarted),
				mk("alpha", lsp.StateReady),
				mk("mango", lsp.StateStarting),
				mk("bravo", lsp.StateError),
			},
			want: []string{"alpha", "mango", "bravo", "zebra"},
		},
		{
			name: "active group sorted alphabetically by name",
			in: []workspace.LSPClientInfo{
				mk("gopls", lsp.StateReady),
				mk("rust-analyzer", lsp.StateReady),
				mk("tsserver", lsp.StateStarting),
			},
			want: []string{"gopls", "rust-analyzer", "tsserver"},
		},
		{
			name: "inactive group sorted alphabetically by name",
			in: []workspace.LSPClientInfo{
				mk("zulu", lsp.StateStopped),
				mk("alpha", lsp.StateUnstarted),
				mk("mike", lsp.StateDisabled),
				mk("bravo", lsp.StateError),
			},
			want: []string{"alpha", "bravo", "mike", "zulu"},
		},
		{
			name: "single active mixed with single inactive",
			in: []workspace.LSPClientInfo{
				mk("zulu", lsp.StateStopped),
				mk("alpha", lsp.StateReady),
			},
			want: []string{"alpha", "zulu"},
		},
		{
			name: "empty input",
			in:   nil,
			want: []string{},
		},
		{
			name: "all inactive group sorted alphabetically by name",
			in: []workspace.LSPClientInfo{
				mk("charlie", lsp.StateError),
				mk("alpha", lsp.StateUnstarted),
				mk("bravo", lsp.StateStopped),
			},
			want: []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "Starting counts as active even without Ready",
			in: []workspace.LSPClientInfo{
				mk("alpha", lsp.StateError),
				mk("bravo", lsp.StateStarting),
			},
			want: []string{"bravo", "alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sortLSPs(slices.Values(tt.in))
			gotNames := make([]string, 0, len(got))
			for _, s := range got {
				gotNames = append(gotNames, s.Name)
			}
			if !reflect.DeepEqual(gotNames, tt.want) {
				t.Errorf("sortLSPs() order = %v, want %v", gotNames, tt.want)
			}
		})
	}
}
