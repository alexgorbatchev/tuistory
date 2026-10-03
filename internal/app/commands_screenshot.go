package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/remorses/tuistory/internal/screenshot"
	"github.com/spf13/cobra"
)

type screenshotOptions struct {
	sessionName, output string
	immediate           bool
	render              screenshot.Options
}

func newScreenshotCommand(c *commandContext) *cobra.Command {
	o := &screenshotOptions{}
	screenshotCmd := &cobra.Command{
		Use:   "screenshot",
		Short: "Capture the terminal screen as a PNG image file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return o.run(c)
		},
	}
	screenshotCmd.Flags().StringVarP(&o.sessionName, "session", "s", "", "Session name (required)")
	screenshotCmd.Flags().StringVarP(&o.output, "output", "o", "", "Output file path (default: temp file)")
	screenshotCmd.Flags().IntVar(&o.render.Width, "width", 0, "Image width in pixels")
	screenshotCmd.Flags().IntVar(&o.render.FontSize, "font-size", 14, "Font size in pixels")
	screenshotCmd.Flags().Float64Var(&o.render.LineHeight, "line-height", 1.5, "Line height multiplier")
	screenshotCmd.Flags().StringVar(&o.render.Background, "background", "#1a1b26", "Background color")
	screenshotCmd.Flags().StringVar(&o.render.Foreground, "foreground", "#c0caf5", "Text color")
	screenshotCmd.Flags().Float64Var(&o.render.PixelRatio, "pixel-ratio", 1, "Device pixel ratio")
	screenshotCmd.Flags().IntVar(&o.render.Padding, "padding", 2, "Frame padding in terminal cells")
	screenshotCmd.Flags().StringVar(&o.render.FrameColor, "frame-color", "", "Color of the frame area")
	screenshotCmd.Flags().BoolVar(&o.immediate, "immediate", false, "Don't wait for idle state")
	return screenshotCmd
}

func (o *screenshotOptions) run(c *commandContext) error {
	s, err := c.session(o.sessionName)
	if err != nil {
		return err
	}

	if !o.immediate {
		_ = s.WaitIdle(2 * time.Second)
	}

	data, err := s.RenderScreenshot(o.render)
	if err != nil {
		return fmt.Errorf("Failed to screenshot session %q: %w", o.sessionName, err)
	}

	outputPath := o.output
	if outputPath == "" {
		outputPath = filepath.Join(os.TempDir(), fmt.Sprintf("tuistory-screenshot-%d.png", time.Now().UnixMilli()))
	}

	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("writing screenshot file %q: %w", outputPath, err)
	}

	fmt.Fprint(c.stdout, outputPath)
	return nil
}
