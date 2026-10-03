package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/remorses/tuistory/internal/keys"
	"github.com/spf13/cobra"
)

func newTypeCommand(c *commandContext) *cobra.Command {
	var typeSession string
	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type text into the terminal character by character",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(typeSession)
			if err != nil {
				return err
			}
			if err := s.Type(args[0]); err != nil {
				return err
			}
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	typeCmd.Flags().StringVarP(&typeSession, "session", "s", "", "Session name (required)")
	return typeCmd
}

func newPressCommand(c *commandContext) *cobra.Command {
	var pressSession string
	pressCmd := &cobra.Command{
		Use:   "press <key> [...keys]",
		Short: "Press one or more keys simultaneously (key chord)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(pressSession)
			if err != nil {
				return err
			}

			if err := validateKeyChord(args); err != nil {
				return err
			}

			if err := s.Press(args); err != nil {
				return err
			}
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	pressCmd.Flags().StringVarP(&pressSession, "session", "s", "", "Session name (required)")
	return pressCmd
}

func newClickCommand(c *commandContext) *cobra.Command {
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
			s, err := c.session(clickSession)
			if err != nil {
				return err
			}

			err = s.Click(args[0], clickFirst, time.Duration(clickTimeout)*time.Millisecond)
			if err != nil {
				return err
			}
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	clickCmd.Flags().StringVarP(&clickSession, "session", "s", "", "Session name (required)")
	clickCmd.Flags().BoolVar(&clickFirst, "first", false, "Click first match if multiple found")
	clickCmd.Flags().IntVar(&clickTimeout, "timeout", 5000, "Timeout in milliseconds")
	return clickCmd
}

func newClickAtCommand(c *commandContext) *cobra.Command {
	var clickAtSession string
	clickAtCmd := &cobra.Command{
		Use:   "click-at <x> <y>",
		Short: "Click at specific terminal coordinates (column, row)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(clickAtSession)
			if err != nil {
				return err
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
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	clickAtCmd.Flags().StringVarP(&clickAtSession, "session", "s", "", "Session name (required)")
	return clickAtCmd
}

type scrollOptions struct {
	sessionName string
	x, y        int
}

func newScrollCommand(c *commandContext) *cobra.Command {
	o := &scrollOptions{}
	cmd := &cobra.Command{
		Use:   "scroll <direction> [lines]",
		Short: "Scroll the terminal up or down using mouse wheel events",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(c, cmd, args)
		},
	}
	cmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (required)")
	cmd.Flags().IntVar(&o.x, "x", 0, "X coordinate for scroll event")
	cmd.Flags().IntVar(&o.y, "y", 0, "Y coordinate for scroll event")
	return cmd
}

func (o *scrollOptions) run(c *commandContext, cmd *cobra.Command, args []string) error {
	s, err := c.session(o.sessionName)
	if err != nil {
		return err
	}
	dir := strings.ToLower(args[0])
	if dir != "up" && dir != "down" {
		return fmt.Errorf("Invalid direction: %s. Use \"up\" or \"down\"", args[0])
	}
	count := 1
	if len(args) > 1 {
		value, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid scroll lines %q: %w", args[1], err)
		}
		count = value
	}
	x, y, err := o.coordinates(cmd)
	if err != nil {
		return err
	}
	if dir == "up" {
		err = s.ScrollUp(count, x, y)
	} else {
		err = s.ScrollDown(count, x, y)
	}
	if err != nil {
		return err
	}
	fmt.Fprint(c.stdout, "OK")
	return nil
}

func (o *scrollOptions) coordinates(cmd *cobra.Command) (*int, *int, error) {
	var x, y *int
	if cmd.Flags().Changed("x") {
		if o.x < 0 {
			return nil, nil, fmt.Errorf("x coordinate must be nonnegative")
		}
		x = &o.x
	}
	if cmd.Flags().Changed("y") {
		if o.y < 0 {
			return nil, nil, fmt.Errorf("y coordinate must be nonnegative")
		}
		y = &o.y
	}
	return x, y, nil
}

func newResizeCommand(c *commandContext) *cobra.Command {
	var resizeSession string
	resizeCmd := &cobra.Command{
		Use:   "resize <cols> <rows>",
		Short: "Resize the terminal to new dimensions",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := c.session(resizeSession)
			if err != nil {
				return err
			}
			cols, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid cols %q", args[0])
			}
			r, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid rows %q", args[1])
			}
			if err := s.Resize(cols, r); err != nil {
				return err
			}
			fmt.Fprint(c.stdout, "OK")
			return nil
		},
	}
	resizeCmd.Flags().StringVarP(&resizeSession, "session", "s", "", "Session name (required)")
	return resizeCmd
}

func newCaptureFramesCommand(c *commandContext) *cobra.Command {
	const maxInterval = math.MaxInt64 / int64(time.Millisecond)
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
			s, err := c.session(framesSession)
			if err != nil {
				return err
			}

			if err := validateKeyChord(args); err != nil {
				return err
			}

			if framesInterval < 0 || int64(framesInterval) > maxInterval {
				return fmt.Errorf("interval must be between 0 and %d milliseconds, got %d", maxInterval, framesInterval)
			}
			frames, err := s.CaptureFrames(args, framesCount, time.Duration(framesInterval)*time.Millisecond)
			if err != nil {
				return err
			}

			return c.writeJSON(frames, false)
		},
	}
	captureFramesCmd.Flags().StringVarP(&framesSession, "session", "s", "", "Session name (required)")
	captureFramesCmd.Flags().IntVar(&framesCount, "count", 5, "Number of frames to capture")
	captureFramesCmd.Flags().IntVar(&framesInterval, "interval", 10, "Interval between frames in ms")
	return captureFramesCmd
}

func validateKeyChord(tokens []string) error {
	var invalid []string
	for _, key := range tokens {
		if !keys.IsValidKey(key) {
			invalid = append(invalid, key)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("Invalid key(s): %s\nValid keys: %s", strings.Join(invalid, ", "), strings.Join(keys.ValidKeysList(), ", "))
	}
	return nil
}
