package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
	"github.com/spf13/cobra"
)

// ExecuteCommand executes CLI args against a session registry in daemon/in-memory mode.
func ExecuteCommand(args []string, reg *relay.SessionRegistry, callerCwd string, callerEnv map[string]string) relay.CLIResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	rootCmd := NewCommand(reg, callerCwd, callerEnv, &stdout, &stderr)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if stderr.Len() == 0 {
			stderr.WriteString(err.Error())
		}
	}

	return relay.CLIResult{
		Stdout:   strings.TrimRight(stdout.String(), "\n"),
		Stderr:   strings.TrimRight(stderr.String(), "\n"),
		ExitCode: exitCode,
	}
}

type commandContext struct {
	registry       *relay.SessionRegistry
	cwd            string
	env            map[string]string
	stdout, stderr *bytes.Buffer
}

func (c *commandContext) session(name string) (*session.Session, error) {
	if name == "" {
		return nil, fmt.Errorf("Error: -s/--session is required")
	}
	s := c.registry.Get(name)
	if s == nil {
		return nil, fmt.Errorf("Session %q not found", name)
	}
	return s, nil
}

func (c *commandContext) writeJSON(value any, indent bool) error {
	var data []byte
	var err error
	if indent {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return fmt.Errorf("encoding JSON output: %w", err)
	}
	_, err = c.stdout.Write(data)
	return err
}

// NewCommand constructs the complete Cobra command tree for tuistory.
func NewCommand(reg *relay.SessionRegistry, callerCwd string, callerEnv map[string]string, stdout, stderr *bytes.Buffer) *cobra.Command {
	c := &commandContext{registry: reg, cwd: callerCwd, env: callerEnv, stdout: stdout, stderr: stderr}
	root := newLaunchCommands(c)
	root.AddCommand(
		newSnapshotCommand(c),
		newReadCommand(c),
		newScreenshotCommand(c),
		newTypeCommand(c),
		newPressCommand(c),
		newClickCommand(c),
		newClickAtCommand(c),
		newWaitCommand(c),
		newWaitIdleCommand(c),
		newScrollCommand(c),
		newResizeCommand(c),
		newCaptureFramesCommand(c),
		newCloseCommand(c),
		newRestartCommand(c),
		newSessionsCommand(c),
		newLogfileCommand(c),
		newDaemonStopCommand(c),
		newAttachCommand(c),
	)
	root.SetOut(stdout)
	root.SetErr(stderr)
	setupHelp(root)
	return root
}
