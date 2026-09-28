package common

import (
	"strings"
	"testing"

	"github.com/charmbracelet/crush/internal/clipboard"
	"github.com/charmbracelet/crush/internal/ui/util"
)

// TestClipboardUnreachable walks the terminal × session matrix that decides
// whether crush can put text on the clipboard the user pastes from.
//
// 无参数
// 逐格验证组合：只有「丢弃 OSC 52 的终端」叠加「SSH 会话」才判定为不可达，
// 本地 IDE 与支持 OSC 52 的终端都必须继续按时成功处理。
func TestClipboardUnreachable(t *testing.T) {
	tests := []struct {
		name             string
		terminalEmulator string
		sshTTY           string
		sshConnection    string
		want             bool
	}{
		{
			name:             "jetbrains terminal over ssh tty",
			terminalEmulator: "JetBrains-JediTerm",
			sshTTY:           "/dev/pts/5",
			want:             true,
		},
		{
			name:             "jetbrains terminal over ssh connection",
			terminalEmulator: "JetBrains-JediTerm",
			sshConnection:    "172.21.36.102 50774 10.132.27.56 22",
			want:             true,
		},
		{
			name:             "jetbrains terminal locally",
			terminalEmulator: "JetBrains-JediTerm",
			want:             false,
		},
		{
			name:             "osc52 capable terminal over ssh",
			terminalEmulator: "wezterm",
			sshTTY:           "/dev/pts/5",
			want:             false,
		},
		{
			name:   "unknown terminal over ssh",
			sshTTY: "/dev/pts/5",
			want:   false,
		},
		{
			name: "unknown terminal locally",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Explicit empty values keep the developer's own environment from
			// leaking into the decision.
			t.Setenv("TERMINAL_EMULATOR", tt.terminalEmulator)
			t.Setenv("SSH_TTY", tt.sshTTY)
			t.Setenv("SSH_CONNECTION", tt.sshConnection)

			if got := clipboardUnreachable(); got != tt.want {
				t.Fatalf("clipboardUnreachable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCopyResultMsg checks which message the user gets for each copy outcome.
//
// 无参数
// 覆盖写入失败、远端 JetBrains 终端（含原生剪切板不可用）、本地 JetBrains
// 终端与支持 OSC 52 的终端四种分支，确保只有真正到不了用户手上的复制才被
// 降级为警告，其余仍走成功提示。
func TestCopyResultMsg(t *testing.T) {
	const successMessage = "Message copied to clipboard"

	tests := []struct {
		name             string
		writeErr         error
		terminalEmulator string
		sshTTY           string
		wantWarn         bool
		wantMessage      string
	}{
		{
			name:             "native write failure wins",
			writeErr:         clipboard.ErrWriteFailed,
			terminalEmulator: "JetBrains-JediTerm",
			sshTTY:           "/dev/pts/5",
			wantWarn:         true,
			wantMessage:      "Failed to copy to clipboard",
		},
		{
			name:             "no native clipboard in a remote jetbrains session",
			writeErr:         clipboard.ErrUnsupported,
			terminalEmulator: "JetBrains-JediTerm",
			sshTTY:           "/dev/pts/5",
			wantWarn:         true,
			wantMessage:      jetBrainsClipboardHint,
		},
		{
			name:             "remote jetbrains session",
			terminalEmulator: "JetBrains-JediTerm",
			sshTTY:           "/dev/pts/5",
			wantWarn:         true,
			wantMessage:      jetBrainsClipboardHint,
		},
		{
			name:             "local jetbrains session",
			terminalEmulator: "JetBrains-JediTerm",
		},
		{
			name:             "osc52 capable terminal over ssh",
			terminalEmulator: "wezterm",
			sshTTY:           "/dev/pts/5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERMINAL_EMULATOR", tt.terminalEmulator)
			t.Setenv("SSH_TTY", tt.sshTTY)
			t.Setenv("SSH_CONNECTION", "")

			msg := copyResultMsg(tt.writeErr, successMessage, nil)
			info, isInfo := msg.(util.InfoMsg)
			if !isInfo {
				t.Fatalf("expected an info message, got %T", msg)
			}
			if !tt.wantWarn {
				if info.Type != util.InfoTypeInfo || info.Msg != successMessage {
					t.Fatalf("expected success message %q, got type %v message %q", successMessage, info.Type, info.Msg)
				}
				return
			}
			if info.Type != util.InfoTypeWarn {
				t.Fatalf("expected a warning, got type %v message %q", info.Type, info.Msg)
			}
			if info.Msg != tt.wantMessage {
				t.Fatalf("warning = %q, want %q", info.Msg, tt.wantMessage)
			}
		})
	}
}

// TestJetBrainsClipboardHintStatesWorkaround keeps the hint actionable: it
// must name the missing protocol and the setting that makes copying work.
//
// 无参数
// 校验提示文案同时包含 OSC 52 与 options.tui.mouse。
func TestJetBrainsClipboardHintStatesWorkaround(t *testing.T) {
	for _, want := range []string{"OSC 52", "options.tui.mouse"} {
		if !strings.Contains(jetBrainsClipboardHint, want) {
			t.Errorf("hint %q is missing %q", jetBrainsClipboardHint, want)
		}
	}
}
