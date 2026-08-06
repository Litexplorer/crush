package tools

import (
	"context"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/permission"
)

// TerminalToolName is the name of the terminal tool.
const TerminalToolName = "terminal"

// TerminalParams are the parameters for the terminal tool.
type TerminalParams struct {
	// The command to run in the client's terminal.
	Command string `json:"command" description:"The command to run in the terminal"`
	// Optional arguments for the command.
	Args []string `json:"args,omitempty" description:"Optional arguments for the command"`
	// Optional working directory for the command.
	Cwd string `json:"cwd,omitempty" description:"Working directory for the command"`
}

const terminalDescription = `Runs a command in a terminal on the client (for example the editor's integrated terminal). Prefer this over bash for interactive or long-running commands when a client terminal is available. When the client has no terminal support, use the bash tool instead.

Parameters:
- command: the command to run
- args: optional arguments
- cwd: optional working directory`

// NewTerminalTool returns a tool that runs commands in a client-provided
// terminal when a TerminalRunner is configured; otherwise it reports
// that client terminals are unavailable. Like bash, command execution
// goes through the permission service.
func NewTerminalTool(runner config.TerminalRunner, permissions permission.Service, workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		TerminalToolName,
		terminalDescription,
		func(ctx context.Context, params TerminalParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Command == "" {
				return fantasy.NewTextErrorResponse("command is required"), nil
			}
			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for the terminal tool")
			}
			if runner == nil {
				return fantasy.NewTextErrorResponse("Client terminals are unavailable; use the bash tool instead"), nil
			}

			dir := params.Cwd
			if dir == "" {
				dir = workingDir
			}
			granted, permReqErr := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        dir,
					ToolCallID:  call.ID,
					ToolName:    TerminalToolName,
					Action:      "execute",
					Description: fmt.Sprintf("Execute command in client terminal: %s", params.Command),
					Params:      params,
				},
			)
			if permReqErr != nil {
				return fantasy.ToolResponse{}, permReqErr
			}
			if !granted {
				return NewPermissionDeniedResponse(), nil
			}

			output, exitCode, err := runner.RunTerminal(ctx, sessionID, params.Command, params.Args, params.Cwd)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Terminal error: %v\nUse the bash tool for local shell execution.", err)), nil
			}
			var sb strings.Builder
			sb.WriteString("<terminal>\n")
			if output != "" {
				sb.WriteString(output)
				if !strings.HasSuffix(output, "\n") {
					sb.WriteString("\n")
				}
			}
			sb.WriteString(fmt.Sprintf("</terminal>\n(exit code %d)", exitCode))
			return fantasy.NewTextResponse(sb.String()), nil
		},
	)
}
