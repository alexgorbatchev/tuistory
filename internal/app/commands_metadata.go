package app

import (
	"fmt"

	"github.com/remorses/tuistory/internal/relay"
	"github.com/spf13/cobra"
)

func newLogfileCommand(c *commandContext) *cobra.Command {
	logfileCmd := &cobra.Command{
		Use:   "logfile",
		Short: "Print the path to the daemon log file",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(c.stdout, relay.LogFilePath())
			return nil
		},
	}
	return logfileCmd
}

func newDaemonStopCommand(c *commandContext) *cobra.Command {
	daemonStopCmd := &cobra.Command{
		Use:   "daemon-stop",
		Short: "Stop the background relay daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(c.stdout, "daemon-stop command must be run client-side, not through relay")
			return nil
		},
	}
	return daemonStopCmd
}

func newAttachCommand(c *commandContext) *cobra.Command {
	var sessionName string
	attachCmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach to a running session with an interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(c.stdout, "attach command must be run client-side, not through relay")
			return nil
		},
	}
	attachCmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session name")
	return attachCmd
}
