package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
	"github.com/spf13/cobra"
)

func newCloseCommand(c *commandContext) *cobra.Command {
	var closeSession string
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Close a terminal session and kill its process",
		RunE: func(cmd *cobra.Command, args []string) error {
			if closeSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			if err := c.registry.Close(closeSession, "user-closed"); err != nil {
				return err
			}
			fmt.Fprintf(c.stdout, "Session %q closed", closeSession)
			return nil
		},
	}
	closeCmd.Flags().StringVarP(&closeSession, "session", "s", "", "Session name (required)")
	return closeCmd
}

type restartOptions struct {
	sessionName string
	timeout     int
	noWait      bool
}

func newRestartCommand(c *commandContext) *cobra.Command {
	o := &restartOptions{}
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart a session with the same command, cwd, and environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(c)
		},
	}
	restartCmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (required)")
	restartCmd.Flags().IntVar(&o.timeout, "timeout", 5000, "Timeout for graceful shutdown in milliseconds")
	restartCmd.Flags().BoolVar(&o.noWait, "no-wait", false, "Don't wait for initial data after restart")
	return restartCmd
}

func (o *restartOptions) run(c *commandContext) error {
	if o.sessionName == "" {
		return fmt.Errorf("Error: -s/--session is required")
	}
	newSess, err := c.registry.Restart(o.sessionName, o.prepare)
	if err != nil {
		return fmt.Errorf("Failed to restart session %q: %w", o.sessionName, err)
	}

	if !o.noWait {
		if err := o.waitForRestart(c, newSess); err != nil {
			return err
		}
	}

	fmt.Fprintf(c.stdout, "Session %q restarted", o.sessionName)
	return nil
}

func newSessionsCommand(c *commandContext) *cobra.Command {
	var sessionsJSON bool
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List all active sessions with their commands and working directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			list := c.registry.List()
			if len(list) == 0 {
				if sessionsJSON {
					fmt.Fprint(c.stdout, "[]")
				} else {
					fmt.Fprint(c.stdout, "No active sessions")
				}
				return nil
			}

			// Sort by most recently started first
			slices.SortFunc(list, func(a, b relay.SessionInfo) int {
				if b.StartedAt > a.StartedAt {
					return 1
				}
				if b.StartedAt < a.StartedAt {
					return -1
				}
				return 0
			})

			if sessionsJSON {
				return c.writeJSON(list, true)
			}

			lines := formatSessions(list)

			fmt.Fprint(c.stdout, strings.Join(lines, "\n"))
			return nil
		},
	}
	sessionsCmd.Flags().BoolVar(&sessionsJSON, "json", false, "Output as JSON")
	return sessionsCmd
}

func (o *restartOptions) prepare(s *session.Session) session.LaunchOptions {
	origCmd := s.Command()
	origCwd := s.Cwd()
	origCols := s.Cols()
	origRows := s.Rows()
	origEnv := s.Env()

	if !s.IsDead() {
		_ = s.WriteRaw("\x03") // Ctrl+C
		exited := s.WaitForExit(time.Duration(o.timeout) * time.Millisecond)
		if !exited {
			s.KillProcess()
			s.WaitForExit(2 * time.Second)
		}
	}

	return session.LaunchOptions{
		Command:   "sh",
		Args:      []string{"-c", origCmd},
		Cols:      origCols,
		Rows:      origRows,
		Cwd:       origCwd,
		Env:       origEnv,
		Label:     origCmd,
		IdleDelay: 200 * time.Millisecond,
	}
}

func (o *restartOptions) waitForRestart(c *commandContext, s *session.Session) error {
	waitErr := s.WaitForData(5 * time.Second)
	if waitErr != nil {
		if s.IsDead() {
			return waitErr
		}
		fmt.Fprintf(c.stderr, "Session %q restarted, but produced no output within 5000ms.\nThe process is still running in the background.\nIf the command is expected to be silent at startup, pass --no-wait to skip this check.\n", o.sessionName)
	}
	return nil
}

func formatSessions(list []relay.SessionInfo) []string {
	lines := []string{yamlKey("sessions")}
	for _, item := range list {
		statusStr := ansi("32", "alive")
		if item.Dead {
			statusStr = ansi("31", "dead")
		}
		startedStr := timeAgo(item.StartedAt)
		lines = append(lines, fmt.Sprintf("  %s %s %s", ansi("90", "-"), yamlKey("name"), yamlString(item.Name)))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("status"), statusStr))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("started"), ansi("33", startedStr)))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("command"), yamlString(item.Command)))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("cwd"), yamlString(item.Cwd)))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("cols"), yamlNumber(item.Cols)))
		lines = append(lines, fmt.Sprintf("    %s %s", yamlKey("rows"), yamlNumber(item.Rows)))
	}
	return lines
}
