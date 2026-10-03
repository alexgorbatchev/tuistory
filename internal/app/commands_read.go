package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/session"
	"github.com/spf13/cobra"
)

type snapshotOptions struct {
	sessionName string
	json        bool
	trim        bool
	immediate   bool
	bold        bool
	italic      bool
	underline   bool
	fg          string
	bg          string
	noCursor    bool
}

func newSnapshotCommand(c *commandContext) *cobra.Command {
	o := &snapshotOptions{}
	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture the current terminal screen as text",
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(c)
		},
	}
	snapshotCmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (required)")
	snapshotCmd.Flags().BoolVar(&o.json, "json", false, "Output as JSON with metadata")
	snapshotCmd.Flags().BoolVar(&o.trim, "trim", false, "Trim trailing whitespace and empty lines")
	snapshotCmd.Flags().BoolVar(&o.immediate, "immediate", false, "Don't wait for idle state")
	snapshotCmd.Flags().BoolVar(&o.bold, "bold", false, "Only bold text")
	snapshotCmd.Flags().BoolVar(&o.italic, "italic", false, "Only italic text")
	snapshotCmd.Flags().BoolVar(&o.underline, "underline", false, "Only underlined text")
	snapshotCmd.Flags().StringVar(&o.fg, "fg", "", "Only text with foreground color")
	snapshotCmd.Flags().StringVar(&o.bg, "bg", "", "Only text with background color")
	snapshotCmd.Flags().BoolVar(&o.noCursor, "no-cursor", false, "Hide cursor in snapshot output")
	return snapshotCmd
}

type readOptions struct {
	sessionName string
	all         bool
	trim        bool
	follow      bool
	timeout     int
}

func (o *snapshotOptions) run(c *commandContext) error {
	s, err := c.session(o.sessionName)
	if err != nil {
		return err
	}

	filter := o.filter()

	showCursor := !o.noCursor
	txt, err := s.Text(session.TextOptions{
		Only:       filter,
		TrimEnd:    o.trim,
		Immediate:  o.immediate,
		ShowCursor: &showCursor,
	})
	if err != nil {
		return err
	}

	if o.json {
		res := map[string]any{
			"text":    txt,
			"session": o.sessionName,
		}
		if s.IsDead() {
			res["dead"] = true
			if info := s.ExitInfo(); info != nil {
				res["exitCode"] = info.ExitCode
			}
		}
		return c.writeJSON(res, false)
	}
	fmt.Fprint(c.stdout, txt)
	return nil
}

func newReadCommand(c *commandContext) *cobra.Command {
	o := &readOptions{}
	readCmd := &cobra.Command{
		Use:   "read",
		Short: "Read new process output since the last read call",
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(c)
		},
	}
	readCmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (required)")
	readCmd.Flags().BoolVar(&o.all, "all", false, "Return entire buffered output")
	readCmd.Flags().BoolVar(&o.trim, "trim", false, "Trim trailing whitespace and empty lines")
	readCmd.Flags().BoolVar(&o.follow, "follow", false, "Block until new output arrives")
	readCmd.Flags().IntVar(&o.timeout, "timeout", 5000, "Timeout for --follow in milliseconds")
	return readCmd
}

func (o *readOptions) run(c *commandContext) error {
	s, err := c.session(o.sessionName)
	if err != nil {
		return err
	}

	if o.follow && !o.all && !s.HasUnreadOutput() && !s.IsDead() {
		s.WaitForUnreadOutput(time.Duration(o.timeout) * time.Millisecond)
		if !s.HasUnreadOutput() && !s.IsDead() {
			return fmt.Errorf("No new output after %dms", o.timeout)
		}
	}
	var txt string
	if o.all {
		txt = s.ReadAll()
	} else {
		txt = s.Read()
	}
	if o.trim {
		txt = strings.TrimRight(txt, " \t\r\n")
	}
	exitSuffix := ""
	if s.IsDead() {
		if info := s.ExitInfo(); info != nil {
			exitSuffix = fmt.Sprintf("\n[process exited with code %d]", info.ExitCode)
		}
	}
	if txt == "" && o.follow {
		exitSuffix = strings.TrimLeft(exitSuffix, "\n")
	}
	fmt.Fprint(c.stdout, txt+exitSuffix)
	return nil
}

func newWaitCommand(c *commandContext) *cobra.Command {
	var (
		waitSession string
		waitTimeout int
	)
	waitCmd := &cobra.Command{
		Use:   "wait <pattern>",
		Short: "Wait for text or regex pattern to appear in the terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(waitSession)
			if err != nil {
				return err
			}

			txt, err := s.WaitForText(args[0], time.Duration(waitTimeout)*time.Millisecond)
			if err != nil {
				return err
			}

			fmt.Fprint(c.stdout, waitContext(txt, args[0], s.ReadAll()))
			return nil
		},
	}
	waitCmd.Flags().StringVarP(&waitSession, "session", "s", "", "Session name (required)")
	waitCmd.Flags().IntVar(&waitTimeout, "timeout", 5000, "Timeout in milliseconds")
	return waitCmd
}

func newWaitIdleCommand(c *commandContext) *cobra.Command {
	var (
		idleSession string
		idleTimeout int
	)
	waitIdleCmd := &cobra.Command{
		Use:   "wait-idle",
		Short: "Wait for the terminal to stop receiving data (become idle)",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(idleSession)
			if err != nil {
				return err
			}
			_ = s.WaitIdle(time.Duration(idleTimeout) * time.Millisecond)
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	waitIdleCmd.Flags().StringVarP(&idleSession, "session", "s", "", "Session name (required)")
	waitIdleCmd.Flags().IntVar(&idleTimeout, "timeout", 500, "Timeout in milliseconds")
	return waitIdleCmd
}

func (o *snapshotOptions) filter() *session.StyleFilter {
	var filter *session.StyleFilter
	if o.bold || o.italic || o.underline || o.fg != "" || o.bg != "" {
		filter = &session.StyleFilter{
			Foreground: o.fg,
			Background: o.bg,
		}
		if o.bold {
			filter.Bold = boolPtr(true)
		}
		if o.italic {
			filter.Italic = boolPtr(true)
		}
		if o.underline {
			filter.Underline = boolPtr(true)
		}
	}

	return filter
}

func waitContext(txt, pattern, output string) string {
	// Context around match: up to 10 lines before and after match line
	allLines := strings.Split(output, "\n")
	matchIndex := -1
	for i, line := range allLines {
		if strings.Contains(line, pattern) {
			matchIndex = i
			break
		}
	}

	if matchIndex == -1 {
		return strings.TrimRight(txt, " \t\r\n")
	}

	start := max(0, matchIndex-10)
	end := min(len(allLines), matchIndex+11)
	return strings.TrimRight(strings.Join(allLines[start:end], "\n"), " \t\r\n")
}
