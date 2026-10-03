package main

import (
	"fmt"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v2"
	"github.com/remorses/tuistory/internal/agent"
	"github.com/spf13/cobra"
)

var techCatalog = cobrahelptree.TechCatalog{
	"tuistory": {
		Summary:     "Run dev servers and TUIs that AI agents can read, wait on, and type into",
		Description: "Run dev servers and TUIs that AI agents can read, wait on, and type into - command-line interface.",
	},
	"tuistory launch": {
		Summary:     "Launch a new terminal session with a PTY",
		Description: "Spawns command in background daemon with configurable dimensions.",
	},
	"tuistory snapshot": {
		Summary:     "Capture the current terminal screen as text",
		Description: "Returns full text content of terminal buffer with optional style filters.",
	},
	"tuistory read": {
		Summary:     "Read new process output since the last read call",
		Description: "Returns process output stream with ANSI escape codes stripped.",
	},
	"tuistory screenshot": {
		Summary:     "Capture the terminal screen as a PNG image file",
		Description: "Renders terminal buffer to a PNG image file with colors and styling.",
	},
	"tuistory type": {
		Summary:     "Type text into the terminal character by character",
		Description: "Sends each character individually with delay to simulate real typing.",
	},
	"tuistory press": {
		Summary:     "Press one or more keys simultaneously (key chord)",
		Description: "Sends key chords to terminal (enter, ctrl c, tab, etc.).",
	},
	"tuistory click": {
		Summary:     "Click on text matching a pattern in the terminal",
		Description: "Searches terminal screen for pattern and sends mouse click at its position.",
	},
	"tuistory click-at": {
		Summary:     "Click at specific terminal coordinates (column, row)",
		Description: "Sends a mouse click event at given (x, y) 0-based coordinate.",
	},
	"tuistory wait": {
		Summary:     "Wait for text or regex pattern to appear in the terminal",
		Description: "Polls terminal content until pattern matches or timeout is reached.",
	},
	"tuistory wait-idle": {
		Summary:     "Wait for terminal to stop receiving data (become idle)",
		Description: "Waits until no new data has been received for ~200ms.",
	},
	"tuistory scroll": {
		Summary:     "Scroll the terminal up or down using mouse wheel events",
		Description: "Sends SGR mouse scroll events up or down.",
	},
	"tuistory resize": {
		Summary:     "Resize the terminal to new dimensions",
		Description: "Changes terminal width and height, triggering SIGWINCH in running application.",
	},
	"tuistory capture-frames": {
		Summary:     "Capture multiple rapid terminal snapshots after a keypress",
		Description: "Sends key(s) and captures N frames at fixed interval as JSON array.",
	},
	"tuistory close": {
		Summary:     "Close a terminal session and kill its process",
		Description: "Terminates PTY process and removes session from daemon.",
	},
	"tuistory restart": {
		Summary:     "Restart session with same command, cwd, and environment",
		Description: "Gracefully restarts running process and re-runs original command.",
	},
	"tuistory sessions": {
		Summary:     "List all active sessions with commands and working directories",
		Description: "Shows session name, command, cwd, dimensions, and status.",
	},
	"tuistory logfile": {
		Summary:     "Print the path to the daemon log file",
		Description: "Outputs full filesystem path to daemon relay-server.log.",
	},
	"tuistory daemon-stop": {
		Summary:     "Stop the background relay daemon",
		Description: "Stops background relay server and closes all active sessions.",
	},
	"tuistory attach": {
		Summary:     "Attach interactively to a session with full TUI",
		Description: "Connects terminal to session PTY stream with double Ctrl+C/Ctrl+X support.",
	},
	"tuistory skill": {
		Summary:     "Print the embedded SKILL.md usage guide for agents",
		Description: "Outputs full tuistory agent operational manual verbatim.",
	},
}

func setupHelp(cmd *cobra.Command) {
	_ = cobrahelptree.SetupWithOptions(cmd, cobrahelptree.HelpOptions{
		Catalog: techCatalog,
		Tree: cobrahelptree.TreeOptions{
			HideGeneratedCommands: true,
		},
	})
	help := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if agent.IsAgentMode() {
			if _, err := fmt.Fprintln(c.OutOrStdout(), "ALERT: Agents must read `AGENT=1 tuistory skill` before using this tool."); err != nil {
				c.PrintErrln(err)
				return
			}
		}
		help(c, args)
	})
}
