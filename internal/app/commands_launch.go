package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/client"
	"github.com/remorses/tuistory/internal/session"
	"github.com/spf13/cobra"
)

type launchOptions struct {
	sessionName, cwd           string
	cols, rows                 int
	envFlags                   []string
	attach, background, noWait bool
	timeout                    int
}

func newLaunchCommands(c *commandContext) *cobra.Command {
	o := &launchOptions{}
	run := func(cmd *cobra.Command, args []string) error { return o.run(c, cmd, args) }
	root := &cobra.Command{
		Use:           "tuistory",
		Short:         "Run dev servers and TUIs that AI agents can read, wait on, and type into",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && cmd.ArgsLenAtDash() != 0 {
				return fmt.Errorf("Unknown command: %s", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(cmd.Flags().Args()) == 0 {
				return cmd.Help()
			}
			return run(cmd, args)
		},
	}
	o.addFlags(root)
	launch := &cobra.Command{
		Use:   "launch [command]",
		Short: "Launch a new terminal session with a PTY",
		Args:  cobra.ArbitraryArgs,
		RunE:  run,
	}
	o.addFlags(launch)
	root.AddCommand(launch)
	return root
}

func (o *launchOptions) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (defaults to command)")
	cmd.Flags().IntVar(&o.cols, "cols", 120, "Terminal columns")
	cmd.Flags().IntVar(&o.rows, "rows", 36, "Terminal rows")
	cmd.Flags().StringVar(&o.cwd, "cwd", "", "Working directory")
	cmd.Flags().StringArrayVar(&o.envFlags, "env", nil, "Environment variable (repeatable)")
	cmd.Flags().BoolVar(&o.attach, "attach", false, "Deprecated: attach is now automatic in TTY mode")
	cmd.Flags().BoolVar(&o.background, "background", false, "Run in background without attaching")
	cmd.Flags().BoolVar(&o.noWait, "no-wait", false, "Don't wait for initial data")
	cmd.Flags().IntVar(&o.timeout, "timeout", 5000, "Wait timeout in milliseconds")
}

func (o *launchOptions) target(c *commandContext, label string) (string, string) {
	cwd := c.cwd
	if o.cwd != "" {
		if filepath.IsAbs(o.cwd) {
			cwd = o.cwd
		} else {
			cwd = filepath.Join(c.cwd, o.cwd)
		}
	}
	name := o.sessionName
	if name == "" {
		name = client.GetDefaultSessionName(label, cwd)
	}
	return name, cwd
}

func (o *launchOptions) environment(c *commandContext, name string) map[string]string {
	env := make(map[string]string)
	for k, v := range c.env {
		env[k] = v
	}
	for _, e := range o.envFlags {
		if i := strings.Index(e, "="); i > 0 {
			env[e[:i]] = e[i+1:]
		}
	}
	env["TUISTORY_SESSION"] = name
	return env
}

func (o *launchOptions) run(c *commandContext, cmd *cobra.Command, args []string) error {
	program, argv, label := LaunchCommand(cmd, args)
	if label == "" {
		return fmt.Errorf("Error: missing command. Use `tuistory -- cmd` or `tuistory launch \"cmd\"`.")
	}
	name, cwd := o.target(c, label)
	c.registry.EvictStaleDead()
	existing := c.registry.Get(name)
	if existing != nil && existing.IsDead() {
		existing.Close("relaunch")
		c.registry.Delete(name)
		existing = nil
	}
	if existing != nil {
		o.printExisting(c, existing, name)
		return nil
	}
	opts := session.LaunchOptions{
		Command: program, Args: argv, Label: label,
		Cols: o.cols, Rows: o.rows, Cwd: cwd,
		Env: o.environment(c, name), IdleDelay: 200 * time.Millisecond,
	}
	s, err := session.New(opts)
	if err != nil {
		return fmt.Errorf("starting session %q: %w", name, err)
	}
	c.registry.Set(name, s)
	if !o.noWait {
		if err := o.waitForLaunch(c, s, name); err != nil {
			return err
		}
	}
	o.printStarted(c, opts, name)
	return nil
}

func (o *launchOptions) waitForLaunch(c *commandContext, s *session.Session, name string) error {
	wait := time.Duration(o.timeout) * time.Millisecond
	if wait <= 0 {
		wait = 5 * time.Second
	}
	if err := s.WaitForData(wait); err != nil {
		if s.IsDead() {
			return fmt.Errorf("Failed to launch session %q: %w", name, err)
		}
		fmt.Fprintf(c.stderr, "Session %q started, but produced no output within %dms.\nThe process is still running in the background.\nIf the command is expected to be silent at startup, pass --no-wait to skip this check.\n", name, o.timeout)
	}
	return nil
}

func (o *launchOptions) printExisting(c *commandContext, s *session.Session, name string) {
	if !o.background {
		fmt.Fprintf(c.stdout, "Session %q already running\n  with command: `%s`\n  in cwd: `%s`\n  read output with: `tuistory read -s %s --all`", name, s.Command(), s.Cwd(), shellQuote(name))
		return
	}
	fmt.Fprintf(c.stdout, "Session %q is already running.\n\n", name)
	fmt.Fprintf(c.stdout, "  command: %s\n  cwd:     %s\n  cols:    %d\n  rows:    %d\n\n", s.Command(), s.Cwd(), s.Cols(), s.Rows())
	fmt.Fprint(c.stdout, "Use these commands to interact with the session:\n\n")
	c.printInteractionCommands(name, false)
}

func (o *launchOptions) printStarted(c *commandContext, opts session.LaunchOptions, name string) {
	if !o.background {
		fmt.Fprintf(c.stdout, "Session %q started", name)
		return
	}
	fmt.Fprintf(c.stdout, "Session %q is now running in the background.\n\n", name)
	fmt.Fprintf(c.stdout, "  command: %s\n  cwd:     %s\n  cols:    %d\n  rows:    %d\n\n", opts.Label, opts.Cwd, opts.Cols, opts.Rows)
	fmt.Fprint(c.stdout, "The process is alive but you are not attached to it.\nUse these commands to interact with the session:\n\n")
	c.printInteractionCommands(name, true)
}

func (c *commandContext) printInteractionCommands(name string, fresh bool) {
	q := shellQuote(name)
	fmt.Fprintf(c.stdout, "  tuistory read -s %s            # read new output since last read\n", q)
	fmt.Fprintf(c.stdout, "  tuistory read -s %s --all      # read entire output buffer\n", q)
	fmt.Fprintf(c.stdout, "  tuistory -s %s wait \"pattern\"  # wait for text to appear (supports /regex/)\n", q)
	if fresh {
		fmt.Fprintf(c.stdout, "  tuistory -s %s wait-idle       # wait until output stabilizes\n", q)
	}
	fmt.Fprintf(c.stdout, "  tuistory -s %s snapshot --trim # current terminal screen as text\n", q)
	fmt.Fprintf(c.stdout, "  tuistory -s %s type \"text\"     # type into the process\n", q)
	fmt.Fprintf(c.stdout, "  tuistory -s %s press enter     # press a key (enter, ctrl c, tab, ...)\n", q)
	fmt.Fprintf(c.stdout, "  tuistory attach -s %s          # attach interactively (fullscreen TUI)\n", q)
	if fresh {
		fmt.Fprintf(c.stdout, "  tuistory -s %s restart         # restart the process\n", q)
	}
	fmt.Fprintf(c.stdout, "  tuistory -s %s close           # kill process and remove session\n\n", q)
	fmt.Fprint(c.stdout, "Run tuistory --help for the full command reference.")
}
