package model

import (
	"reflect"
	"slices"
	"testing"

	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/lsp"
)

func TestSortLSPsByActivation(t *testing.T) {
	t.Parallel()

	mk := func(name string, state lsp.ServerState) app.LSPClientInfo {
		return app.LSPClientInfo{Name: name, State: state}
	}

	tests := []struct {
		name string
		in   []app.LSPClientInfo
		want []string
	}{
		{
			name: "active states (Ready, Starting) sort before inactive",
			in: []app.LSPClientInfo{
				mk("zebra", lsp.StateUnstarted),
				mk("alpha", lsp.StateReady),
				mk("mango", lsp.StateStarting),
				mk("bravo", lsp.StateError),
			},
			want: []string{"alpha", "mango", "bravo", "zebra"},
		},
		{
			name: "active group sorted alphabetically by name",
			in: []app.LSPClientInfo{
				mk("gopls", lsp.StateReady),
				mk("rust-analyzer", lsp.StateReady),
				mk("tsserver", lsp.StateStarting),
			},
			want: []string{"gopls", "rust-analyzer", "tsserver"},
		},
		{
			name: "inactive group sorted alphabetically by name",
			in: []app.LSPClientInfo{
				mk("zulu", lsp.StateStopped),
				mk("alpha", lsp.StateUnstarted),
				mk("mike", lsp.StateDisabled),
				mk("bravo", lsp.StateError),
			},
			want: []string{"alpha", "bravo", "mike", "zulu"},
		},
		{
			name: "single active mixed with single inactive",
			in: []app.LSPClientInfo{
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
			name: "all inactive preserves alphabetical order",
			in: []app.LSPClientInfo{
				mk("charlie", lsp.StateError),
				mk("alpha", lsp.StateUnstarted),
				mk("bravo", lsp.StateStopped),
			},
			want: []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "Starting counts as active even without Ready",
			in: []app.LSPClientInfo{
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
