package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/charmbracelet/crush/internal/acp"
	"github.com/charmbracelet/crush/internal/backend"
	"github.com/charmbracelet/crush/internal/config"
	crushlog "github.com/charmbracelet/crush/internal/log"
	"github.com/charmbracelet/x/term"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(acpCmd)
}

var acpCmd = &cobra.Command{
	Use:   "acp",
	Short: "Start the Crush ACP agent",
	Long: `Start the Crush ACP agent on stdio.

The agent speaks the Agent Client Protocol (ACP) over stdin/stdout using
ND-JSON JSON-RPC, so it can be driven by ACP clients such as Zed.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}
		dataDir, err := cmd.Flags().GetString("data-dir")
		if err != nil {
			return fmt.Errorf("failed to get data directory: %v", err)
		}
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return fmt.Errorf("failed to get debug flag: %v", err)
		}

		cfg, err := config.Load(config.GlobalWorkspaceDir(), dataDir, debug)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %v", err)
		}

		logFile := filepath.Join(config.GlobalCacheDir(), "acp", "crush.log")
		if term.IsTerminal(os.Stderr.Fd()) {
			crushlog.Setup(logFile, debug, os.Stderr)
		} else {
			crushlog.Setup(logFile, debug)
		}

		slog.Info("Starting Crush ACP agent", "cwd", cwd)

		// The ACP connection drives the process lifetime, so the
		// backend is created without an idle-shutdown callback.
		agent := acp.NewAgent(backend.New(context.Background(), cfg, nil), cfg.Config().Options.DataDirectory)
		conn := acpsdk.NewAgentSideConnection(agent, os.Stdout, os.Stdin)
		agent.Attach(conn)
		conn.SetLogger(slog.Default())

		// Block until the client disconnects (stdin EOF).
		<-conn.Done()
		agent.CloseAll()
		slog.Info("ACP client disconnected, shutting down")
		return nil
	},
}
