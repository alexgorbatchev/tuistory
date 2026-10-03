package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/client"
	"github.com/remorses/tuistory/internal/keys"
	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/screenshot"
	"github.com/remorses/tuistory/internal/session"
	"github.com/spf13/cobra"
)

const appVersion = "0.0.1"

// ExecuteCommand executes CLI args against a session registry in daemon/in-memory mode.
func ExecuteCommand(args []string, reg *relay.SessionRegistry, callerCwd string, callerEnv map[string]string) relay.CLIResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	rootCmd := NewCommand(reg, callerCwd, callerEnv, &stdout, &stderr)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	// If `--` is present in args:
	// We handle `--` specially because Cobra stops parsing flags at `--`
	// but puts remainder into Args.
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

// NewCommand constructs the complete Cobra command tree for tuistory.
func NewCommand(reg *relay.SessionRegistry, callerCwd string, callerEnv map[string]string, stdout, stderr *bytes.Buffer) *cobra.Command {
	var (
		sessionName string
		cols        int
		rows        int
		cwd         string
		envFlags    []string
		attach      bool
		background  bool
		noWait      bool
		timeout     int
	)

	launchAction := func(cmd *cobra.Command, rawArgs []string) error {
		program, programArgs, label := LaunchCommand(cmd, rawArgs)
		launchCmd := label

		if launchCmd == "" {
			return fmt.Errorf("Error: missing command. Use `tuistory -- cmd` or `tuistory launch \"cmd\"`.")
		}

		targetCwd := callerCwd
		if cwd != "" {
			if filepath.IsAbs(cwd) {
				targetCwd = cwd
			} else {
				targetCwd = filepath.Join(callerCwd, cwd)
			}
		}

		targetSession := sessionName
		if targetSession == "" {
			targetSession = client.GetDefaultSessionName(launchCmd, targetCwd)
		}

		// Evict dead sessions older than 24h
		reg.EvictStaleDead()

		existing := reg.Get(targetSession)
		if existing != nil && existing.IsDead() {
			existing.Close("relaunch")
			reg.Delete(targetSession)
			existing = nil
		}

		if existing != nil {
			q := shellQuote(targetSession)
			if background {
				fmt.Fprintf(stdout, "Session %q is already running.\n\n", targetSession)
				fmt.Fprintf(stdout, "  command: %s\n", existing.Command())
				fmt.Fprintf(stdout, "  cwd:     %s\n", existing.Cwd())
				fmt.Fprintf(stdout, "  cols:    %d\n", existing.Cols())
				fmt.Fprintf(stdout, "  rows:    %d\n\n", existing.Rows())
				fmt.Fprintf(stdout, "Use these commands to interact with the session:\n\n")
				fmt.Fprintf(stdout, "  tuistory read -s %s            # read new output since last read\n", q)
				fmt.Fprintf(stdout, "  tuistory read -s %s --all      # read entire output buffer\n", q)
				fmt.Fprintf(stdout, "  tuistory -s %s wait \"pattern\"  # wait for text to appear (supports /regex/)\n", q)
				fmt.Fprintf(stdout, "  tuistory -s %s snapshot --trim # current terminal screen as text\n", q)
				fmt.Fprintf(stdout, "  tuistory -s %s type \"text\"     # type into the process\n", q)
				fmt.Fprintf(stdout, "  tuistory -s %s press enter     # press a key (enter, ctrl c, tab, ...)\n", q)
				fmt.Fprintf(stdout, "  tuistory attach -s %s          # attach interactively (fullscreen TUI)\n", q)
				fmt.Fprintf(stdout, "  tuistory -s %s close           # kill process and remove session\n\n", q)
				fmt.Fprint(stdout, "Run tuistory --help for the full command reference.")
			} else {
				fmt.Fprintf(stdout, "Session %q already running\n  with command: `%s`\n  in cwd: `%s`\n  read output with: `tuistory read -s %s --all`", targetSession, existing.Command(), existing.Cwd(), q)
			}
			return nil
		}

		mergedEnv := make(map[string]string)
		for k, v := range callerEnv {
			mergedEnv[k] = v
		}
		for _, e := range envFlags {
			if idx := strings.Index(e, "="); idx > 0 {
				mergedEnv[e[:idx]] = e[idx+1:]
			}
		}
		mergedEnv["TUISTORY_SESSION"] = targetSession

		sess, err := session.New(session.LaunchOptions{
			Command:   program,
			Args:      programArgs,
			Cols:      cols,
			Rows:      rows,
			Cwd:       targetCwd,
			Env:       mergedEnv,
			Label:     launchCmd,
			IdleDelay: 200 * time.Millisecond,
		})
		if err != nil {
			return fmt.Errorf("starting session %q: %w", targetSession, err)
		}

		reg.Set(targetSession, sess)

		if !noWait {
			waitDuration := time.Duration(timeout) * time.Millisecond
			if waitDuration <= 0 {
				waitDuration = 5 * time.Second
			}
			waitErr := sess.WaitForData(waitDuration)
			if waitErr != nil {
				if sess.IsDead() {
					return fmt.Errorf("Failed to launch session %q: %w", targetSession, waitErr)
				}
				fmt.Fprintf(stderr, "Session %q started, but produced no output within %dms.\nThe process is still running in the background.\nIf the command is expected to be silent at startup, pass --no-wait to skip this check.\n", targetSession, timeout)
			}
		}

		if background {
			q := shellQuote(targetSession)
			fmt.Fprintf(stdout, "Session %q is now running in the background.\n\n", targetSession)
			fmt.Fprintf(stdout, "  command: %s\n", launchCmd)
			fmt.Fprintf(stdout, "  cwd:     %s\n", targetCwd)
			fmt.Fprintf(stdout, "  cols:    %d\n", cols)
			fmt.Fprintf(stdout, "  rows:    %d\n\n", rows)
			fmt.Fprintf(stdout, "The process is alive but you are not attached to it.\nUse these commands to interact with the session:\n\n")
			fmt.Fprintf(stdout, "  tuistory read -s %s            # read new output since last read\n", q)
			fmt.Fprintf(stdout, "  tuistory read -s %s --all      # read entire output buffer\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s wait \"pattern\"  # wait for text to appear (supports /regex/)\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s wait-idle       # wait until output stabilizes\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s snapshot --trim # current terminal screen as text\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s type \"text\"     # type into the process\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s press enter     # press a key (enter, ctrl c, tab, ...)\n", q)
			fmt.Fprintf(stdout, "  tuistory attach -s %s          # attach interactively (fullscreen TUI)\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s restart         # restart the process\n", q)
			fmt.Fprintf(stdout, "  tuistory -s %s close           # kill process and remove session\n\n", q)
			fmt.Fprint(stdout, "Run tuistory --help for the full command reference.")
		} else {
			fmt.Fprintf(stdout, "Session %q started", targetSession)
		}

		return nil
	}

	rootCmd := &cobra.Command{
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
			// If bare positional arguments exist (without -- or launch), reject them!
			// Notice: Cobra puts args before and after -- into args if no subcommand matched.
			// If args contains positional args that were NOT preceded by --, fail with Unknown command.
			dashArgs := cmd.Flags().Args()
			if len(dashArgs) == 0 {
				return cmd.Help()
			}
			return launchAction(cmd, args)
		},
	}

	addLaunchFlags := func(cmd *cobra.Command) {
		cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session name (defaults to command)")
		cmd.Flags().IntVar(&cols, "cols", 120, "Terminal columns")
		cmd.Flags().IntVar(&rows, "rows", 36, "Terminal rows")
		cmd.Flags().StringVar(&cwd, "cwd", "", "Working directory")
		cmd.Flags().StringArrayVar(&envFlags, "env", nil, "Environment variable (repeatable)")
		cmd.Flags().BoolVar(&attach, "attach", false, "Deprecated: attach is now automatic in TTY mode")
		cmd.Flags().BoolVar(&background, "background", false, "Run in background without attaching")
		cmd.Flags().BoolVar(&noWait, "no-wait", false, "Don't wait for initial data")
		cmd.Flags().IntVar(&timeout, "timeout", 5000, "Wait timeout in milliseconds")
	}

	addLaunchFlags(rootCmd)

	// launch command
	launchCmd := &cobra.Command{
		Use:   "launch [command]",
		Short: "Launch a new terminal session with a PTY",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return launchAction(cmd, args)
		},
	}
	addLaunchFlags(launchCmd)
	rootCmd.AddCommand(launchCmd)

	// snapshot command
	var (
		snapSession   string
		snapJSON      bool
		snapTrim      bool
		snapImmediate bool
		snapBold      bool
		snapItalic    bool
		snapUnderline bool
		snapFg        string
		snapBg        string
		snapNoCursor  bool
	)
	snapshotCmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture the current terminal screen as text",
		RunE: func(cmd *cobra.Command, args []string) error {
			if snapSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(snapSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", snapSession)
			}

			var filter *session.StyleFilter
			if snapBold || snapItalic || snapUnderline || snapFg != "" || snapBg != "" {
				filter = &session.StyleFilter{
					Foreground: snapFg,
					Background: snapBg,
				}
				if snapBold {
					filter.Bold = boolPtr(true)
				}
				if snapItalic {
					filter.Italic = boolPtr(true)
				}
				if snapUnderline {
					filter.Underline = boolPtr(true)
				}
			}

			showCursor := !snapNoCursor
			txt, err := s.Text(session.TextOptions{
				Only:       filter,
				TrimEnd:    snapTrim,
				Immediate:  snapImmediate,
				ShowCursor: &showCursor,
			})
			if err != nil {
				return err
			}

			if snapJSON {
				res := map[string]any{
					"text":    txt,
					"session": snapSession,
				}
				if s.IsDead() {
					res["dead"] = true
					if info := s.ExitInfo(); info != nil {
						res["exitCode"] = info.ExitCode
					}
				}
				encoded, _ := json.Marshal(res)
				fmt.Fprint(stdout, string(encoded))
			} else {
				fmt.Fprint(stdout, txt)
			}
			return nil
		},
	}
	snapshotCmd.Flags().StringVarP(&snapSession, "session", "s", "", "Session name (required)")
	snapshotCmd.Flags().BoolVar(&snapJSON, "json", false, "Output as JSON with metadata")
	snapshotCmd.Flags().BoolVar(&snapTrim, "trim", false, "Trim trailing whitespace and empty lines")
	snapshotCmd.Flags().BoolVar(&snapImmediate, "immediate", false, "Don't wait for idle state")
	snapshotCmd.Flags().BoolVar(&snapBold, "bold", false, "Only bold text")
	snapshotCmd.Flags().BoolVar(&snapItalic, "italic", false, "Only italic text")
	snapshotCmd.Flags().BoolVar(&snapUnderline, "underline", false, "Only underlined text")
	snapshotCmd.Flags().StringVar(&snapFg, "fg", "", "Only text with foreground color")
	snapshotCmd.Flags().StringVar(&snapBg, "bg", "", "Only text with background color")
	snapshotCmd.Flags().BoolVar(&snapNoCursor, "no-cursor", false, "Hide cursor in snapshot output")
	rootCmd.AddCommand(snapshotCmd)

	// read command
	var (
		readSession string
		readAll     bool
		readTrim    bool
		readFollow  bool
		readTimeout int
	)
	readCmd := &cobra.Command{
		Use:   "read",
		Short: "Read new process output since the last read call",
		RunE: func(cmd *cobra.Command, args []string) error {
			if readSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(readSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", readSession)
			}

			if readFollow && !readAll && !s.HasUnreadOutput() && !s.IsDead() {
				s.WaitForUnreadOutput(time.Duration(readTimeout) * time.Millisecond)
				if !s.HasUnreadOutput() && !s.IsDead() {
					return fmt.Errorf("No new output after %dms", readTimeout)
				}
			}
			var txt string
			if readAll {
				txt = s.ReadAll()
			} else {
				txt = s.Read()
			}
			if readTrim {
				txt = strings.TrimRight(txt, " \t\r\n")
			}
			exitSuffix := ""
			if s.IsDead() {
				if info := s.ExitInfo(); info != nil {
					exitSuffix = fmt.Sprintf("\n[process exited with code %d]", info.ExitCode)
				}
			}
			if txt == "" && readFollow {
				exitSuffix = strings.TrimLeft(exitSuffix, "\n")
			}
			fmt.Fprint(stdout, txt+exitSuffix)
			return nil
		},
	}
	readCmd.Flags().StringVarP(&readSession, "session", "s", "", "Session name (required)")
	readCmd.Flags().BoolVar(&readAll, "all", false, "Return entire buffered output")
	readCmd.Flags().BoolVar(&readTrim, "trim", false, "Trim trailing whitespace and empty lines")
	readCmd.Flags().BoolVar(&readFollow, "follow", false, "Block until new output arrives")
	readCmd.Flags().IntVar(&readTimeout, "timeout", 5000, "Timeout for --follow in milliseconds")
	rootCmd.AddCommand(readCmd)

	// screenshot command
	var (
		shotSession    string
		shotOutput     string
		shotWidth      int
		shotFontSize   int
		shotLineHeight float64
		shotBackground string
		shotForeground string
		shotPixelRatio float64
		shotPadding    int
		shotFrameColor string
		shotImmediate  bool
	)
	screenshotCmd := &cobra.Command{
		Use:   "screenshot",
		Short: "Capture the terminal screen as a PNG image file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if shotSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(shotSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", shotSession)
			}

			if !shotImmediate {
				_ = s.WaitIdle(2 * time.Second)
			}

			data, err := s.RenderScreenshot(screenshot.Options{
				Width:      shotWidth,
				FontSize:   shotFontSize,
				LineHeight: shotLineHeight,
				Background: shotBackground,
				Foreground: shotForeground,
				PixelRatio: shotPixelRatio,
				Padding:    shotPadding,
				FrameColor: shotFrameColor,
			})
			if err != nil {
				return fmt.Errorf("Failed to screenshot session %q: %w", shotSession, err)
			}

			outputPath := shotOutput
			if outputPath == "" {
				outputPath = filepath.Join(os.TempDir(), fmt.Sprintf("tuistory-screenshot-%d.png", time.Now().UnixMilli()))
			}

			if err := os.WriteFile(outputPath, data, 0644); err != nil {
				return fmt.Errorf("writing screenshot file %q: %w", outputPath, err)
			}

			fmt.Fprint(stdout, outputPath)
			return nil
		},
	}
	screenshotCmd.Flags().StringVarP(&shotSession, "session", "s", "", "Session name (required)")
	screenshotCmd.Flags().StringVarP(&shotOutput, "output", "o", "", "Output file path (default: temp file)")
	screenshotCmd.Flags().IntVar(&shotWidth, "width", 0, "Image width in pixels")
	screenshotCmd.Flags().IntVar(&shotFontSize, "font-size", 14, "Font size in pixels")
	screenshotCmd.Flags().Float64Var(&shotLineHeight, "line-height", 1.5, "Line height multiplier")
	screenshotCmd.Flags().StringVar(&shotBackground, "background", "#1a1b26", "Background color")
	screenshotCmd.Flags().StringVar(&shotForeground, "foreground", "#c0caf5", "Text color")
	screenshotCmd.Flags().Float64Var(&shotPixelRatio, "pixel-ratio", 1, "Device pixel ratio")
	screenshotCmd.Flags().IntVar(&shotPadding, "padding", 2, "Frame padding in terminal cells")
	screenshotCmd.Flags().StringVar(&shotFrameColor, "frame-color", "", "Color of the frame area")
	screenshotCmd.Flags().BoolVar(&shotImmediate, "immediate", false, "Don't wait for idle state")
	rootCmd.AddCommand(screenshotCmd)

	// type command
	var typeSession string
	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type text into the terminal character by character",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if typeSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(typeSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", typeSession)
			}
			if err := s.Type(args[0]); err != nil {
				return err
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	typeCmd.Flags().StringVarP(&typeSession, "session", "s", "", "Session name (required)")
	rootCmd.AddCommand(typeCmd)

	// press command
	var pressSession string
	pressCmd := &cobra.Command{
		Use:   "press <key> [...keys]",
		Short: "Press one or more keys simultaneously (key chord)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pressSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(pressSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", pressSession)
			}

			var invalid []string
			for _, k := range args {
				if !keys.IsValidKey(k) {
					invalid = append(invalid, k)
				}
			}
			if len(invalid) > 0 {
				return fmt.Errorf("Invalid key(s): %s\nValid keys: %s", strings.Join(invalid, ", "), strings.Join(keys.ValidKeysList(), ", "))
			}

			if err := s.Press(args); err != nil {
				return err
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	pressCmd.Flags().StringVarP(&pressSession, "session", "s", "", "Session name (required)")
	rootCmd.AddCommand(pressCmd)

	// click command
	var (
		clickSession string
		clickFirst   bool
		clickTimeout int
	)
	clickCmd := &cobra.Command{
		Use:   "click <pattern>",
		Short: "Click on text matching a pattern in the terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clickSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(clickSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", clickSession)
			}

			err := s.Click(args[0], clickFirst, time.Duration(clickTimeout)*time.Millisecond)
			if err != nil {
				return err
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	clickCmd.Flags().StringVarP(&clickSession, "session", "s", "", "Session name (required)")
	clickCmd.Flags().BoolVar(&clickFirst, "first", false, "Click first match if multiple found")
	clickCmd.Flags().IntVar(&clickTimeout, "timeout", 5000, "Timeout in milliseconds")
	rootCmd.AddCommand(clickCmd)

	// click-at command
	var clickAtSession string
	clickAtCmd := &cobra.Command{
		Use:   "click-at <x> <y>",
		Short: "Click at specific terminal coordinates (column, row)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clickAtSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(clickAtSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", clickAtSession)
			}
			x, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid x coordinate %q", args[0])
			}
			y, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid y coordinate %q", args[1])
			}
			if err := s.ClickAt(x, y); err != nil {
				return err
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	clickAtCmd.Flags().StringVarP(&clickAtSession, "session", "s", "", "Session name (required)")
	rootCmd.AddCommand(clickAtCmd)

	// wait command
	var (
		waitSession string
		waitTimeout int
	)
	waitCmd := &cobra.Command{
		Use:   "wait <pattern>",
		Short: "Wait for text or regex pattern to appear in the terminal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if waitSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(waitSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", waitSession)
			}

			txt, err := s.WaitForText(args[0], time.Duration(waitTimeout)*time.Millisecond)
			if err != nil {
				return err
			}

			// Context around match: up to 10 lines before and after match line
			allLines := strings.Split(s.ReadAll(), "\n")
			matchIndex := -1
			for i, line := range allLines {
				if strings.Contains(line, args[0]) {
					matchIndex = i
					break
				}
			}

			if matchIndex == -1 {
				fmt.Fprint(stdout, strings.TrimRight(txt, " \t\r\n"))
				return nil
			}

			start := max(0, matchIndex-10)
			end := min(len(allLines), matchIndex+11)
			fmt.Fprint(stdout, strings.TrimRight(strings.Join(allLines[start:end], "\n"), " \t\r\n"))
			return nil
		},
	}
	waitCmd.Flags().StringVarP(&waitSession, "session", "s", "", "Session name (required)")
	waitCmd.Flags().IntVar(&waitTimeout, "timeout", 5000, "Timeout in milliseconds")
	rootCmd.AddCommand(waitCmd)

	// wait-idle command
	var (
		idleSession string
		idleTimeout int
	)
	waitIdleCmd := &cobra.Command{
		Use:   "wait-idle",
		Short: "Wait for the terminal to stop receiving data (become idle)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if idleSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(idleSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", idleSession)
			}
			_ = s.WaitIdle(time.Duration(idleTimeout) * time.Millisecond)
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	waitIdleCmd.Flags().StringVarP(&idleSession, "session", "s", "", "Session name (required)")
	waitIdleCmd.Flags().IntVar(&idleTimeout, "timeout", 500, "Timeout in milliseconds")
	rootCmd.AddCommand(waitIdleCmd)

	// scroll command
	var (
		scrollSession string
		scrollX       int
		scrollY       int
	)
	scrollCmd := &cobra.Command{
		Use:   "scroll <direction> [lines]",
		Short: "Scroll the terminal up or down using mouse wheel events",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scrollSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(scrollSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", scrollSession)
			}

			dir := strings.ToLower(args[0])
			if dir != "up" && dir != "down" {
				return fmt.Errorf("Invalid direction: %s. Use \"up\" or \"down\"", args[0])
			}

			count := 1
			if len(args) > 1 {
				c, err := strconv.Atoi(args[1])
				if err == nil && c > 0 {
					count = c
				}
			}

			var xPtr, yPtr *int
			if cmd.Flags().Changed("x") {
				xPtr = &scrollX
			}
			if cmd.Flags().Changed("y") {
				yPtr = &scrollY
			}

			if dir == "up" {
				if err := s.ScrollUp(count, xPtr, yPtr); err != nil {
					return err
				}
			} else {
				if err := s.ScrollDown(count, xPtr, yPtr); err != nil {
					return err
				}
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	scrollCmd.Flags().StringVarP(&scrollSession, "session", "s", "", "Session name (required)")
	scrollCmd.Flags().IntVar(&scrollX, "x", 0, "X coordinate for scroll event")
	scrollCmd.Flags().IntVar(&scrollY, "y", 0, "Y coordinate for scroll event")
	rootCmd.AddCommand(scrollCmd)

	// resize command
	var resizeSession string
	resizeCmd := &cobra.Command{
		Use:   "resize <cols> <rows>",
		Short: "Resize the terminal to new dimensions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if resizeSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(resizeSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", resizeSession)
			}
			c, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid cols %q", args[0])
			}
			r, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid rows %q", args[1])
			}
			if err := s.Resize(c, r); err != nil {
				return err
			}
			fmt.Fprint(stdout, "OK")
			return nil
		},
	}
	resizeCmd.Flags().StringVarP(&resizeSession, "session", "s", "", "Session name (required)")
	rootCmd.AddCommand(resizeCmd)

	// capture-frames command
	var (
		framesSession  string
		framesCount    int
		framesInterval int
	)
	captureFramesCmd := &cobra.Command{
		Use:   "capture-frames <key> [...keys]",
		Short: "Capture multiple rapid terminal snapshots after a keypress",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if framesSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(framesSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", framesSession)
			}

			var invalid []string
			for _, k := range args {
				if !keys.IsValidKey(k) {
					invalid = append(invalid, k)
				}
			}
			if len(invalid) > 0 {
				return fmt.Errorf("Invalid key(s): %s\nValid keys: %s", strings.Join(invalid, ", "), strings.Join(keys.ValidKeysList(), ", "))
			}

			frames, err := s.CaptureFrames(args, framesCount, time.Duration(framesInterval)*time.Millisecond)
			if err != nil {
				return err
			}

			encoded, _ := json.Marshal(frames)
			fmt.Fprint(stdout, string(encoded))
			return nil
		},
	}
	captureFramesCmd.Flags().StringVarP(&framesSession, "session", "s", "", "Session name (required)")
	captureFramesCmd.Flags().IntVar(&framesCount, "count", 5, "Number of frames to capture")
	captureFramesCmd.Flags().IntVar(&framesInterval, "interval", 10, "Interval between frames in ms")
	rootCmd.AddCommand(captureFramesCmd)

	// close command
	var closeSession string
	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Close a terminal session and kill its process",
		RunE: func(cmd *cobra.Command, args []string) error {
			if closeSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(closeSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", closeSession)
			}
			s.Close("user-closed")
			reg.Delete(closeSession)
			fmt.Fprintf(stdout, "Session %q closed", closeSession)
			return nil
		},
	}
	closeCmd.Flags().StringVarP(&closeSession, "session", "s", "", "Session name (required)")
	rootCmd.AddCommand(closeCmd)

	// restart command
	var (
		restartSession string
		restartTimeout int
		restartNoWait  bool
	)
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart a session with the same command, cwd, and environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			if restartSession == "" {
				return fmt.Errorf("Error: -s/--session is required")
			}
			s := reg.Get(restartSession)
			if s == nil {
				return fmt.Errorf("Session %q not found", restartSession)
			}

			origCmd := s.Command()
			origCwd := s.Cwd()
			origCols := s.Cols()
			origRows := s.Rows()
			origEnv := s.Env()

			if !s.IsDead() {
				_ = s.WriteRaw("\x03") // Ctrl+C
				exited := s.WaitForExit(time.Duration(restartTimeout) * time.Millisecond)
				if !exited {
					s.KillProcess()
					s.WaitForExit(2 * time.Second)
				}
			}

			s.Close("session-restarting")
			reg.DeleteIfMatches(restartSession, s)

			newSess, err := session.New(session.LaunchOptions{
				Command:   "sh",
				Args:      []string{"-c", origCmd},
				Cols:      origCols,
				Rows:      origRows,
				Cwd:       origCwd,
				Env:       origEnv,
				Label:     origCmd,
				IdleDelay: 200 * time.Millisecond,
			})
			if err != nil {
				return fmt.Errorf("Failed to restart session %q: %w", restartSession, err)
			}

			reg.Set(restartSession, newSess)

			if !restartNoWait {
				waitErr := newSess.WaitForData(5 * time.Second)
				if waitErr != nil {
					if newSess.IsDead() {
						return waitErr
					}
					fmt.Fprintf(stderr, "Session %q restarted, but produced no output within 5000ms.\nThe process is still running in the background.\nIf the command is expected to be silent at startup, pass --no-wait to skip this check.\n", restartSession)
				}
			}

			fmt.Fprintf(stdout, "Session %q restarted", restartSession)
			return nil
		},
	}
	restartCmd.Flags().StringVarP(&restartSession, "session", "s", "", "Session name (required)")
	restartCmd.Flags().IntVar(&restartTimeout, "timeout", 5000, "Timeout for graceful shutdown in milliseconds")
	restartCmd.Flags().BoolVar(&restartNoWait, "no-wait", false, "Don't wait for initial data after restart")
	rootCmd.AddCommand(restartCmd)

	// sessions command
	var sessionsJSON bool
	sessionsCmd := &cobra.Command{
		Use:   "sessions",
		Short: "List all active sessions with their commands and working directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			list := reg.List()
			if len(list) == 0 {
				if sessionsJSON {
					fmt.Fprint(stdout, "[]")
				} else {
					fmt.Fprint(stdout, "No active sessions")
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
				encoded, _ := json.MarshalIndent(list, "", "  ")
				fmt.Fprint(stdout, string(encoded))
				return nil
			}

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

			fmt.Fprint(stdout, strings.Join(lines, "\n"))
			return nil
		},
	}
	sessionsCmd.Flags().BoolVar(&sessionsJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(sessionsCmd)

	// logfile command
	logfileCmd := &cobra.Command{
		Use:   "logfile",
		Short: "Print the path to the daemon log file",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(stdout, relay.LogFilePath())
			return nil
		},
	}
	rootCmd.AddCommand(logfileCmd)

	// daemon-stop command (informational only inside daemon; handled client side)
	daemonStopCmd := &cobra.Command{
		Use:   "daemon-stop",
		Short: "Stop the background relay daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(stdout, "daemon-stop command must be run client-side, not through relay")
			return nil
		},
	}
	rootCmd.AddCommand(daemonStopCmd)

	// attach command (informational only inside daemon; handled client side)
	attachCmd := &cobra.Command{
		Use:   "attach",
		Short: "Attach to a running session with an interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(stdout, "attach command must be run client-side, not through relay")
			return nil
		},
	}
	attachCmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session name")
	rootCmd.AddCommand(attachCmd)

	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	setupHelp(rootCmd)
	return rootCmd
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '/' || c == ':' || c == '-') {
			return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
		}
	}
	return s
}

func yamlKey(key string) string {
	return fmt.Sprintf("%s%s", ansi("36", key), ansi("90", ":"))
}

func yamlString(value string) string {
	quote := ansi("90", "\"")
	escaped := strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "\"", "\\\"")
	return fmt.Sprintf("%s%s%s", quote, ansi("32", escaped), quote)
}

func yamlNumber(value int) string {
	return ansi("35", strconv.Itoa(value))
}

func ansi(code, val string) string {
	return fmt.Sprintf("\x1b[%sm%s\x1b[39m", code, val)
}

func timeAgo(ts int64) string {
	secs := int((time.Now().UnixMilli() - ts) / 1000)
	if secs < 60 {
		return fmt.Sprintf("%ds ago", secs)
	}
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm ago", mins)
	}
	hrs := mins / 60
	if hrs < 24 {
		return fmt.Sprintf("%dh ago", hrs)
	}
	days := hrs / 24
	return fmt.Sprintf("%dd ago", days)
}

func boolPtr(b bool) *bool {
	return &b
}
