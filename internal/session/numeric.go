package session

import (
	"fmt"
	"math"

	"github.com/gitpod-io/xterm-go"
)

func validateDimensions(cols, rows int) error {
	if cols < xterm.MinimumCols || cols > math.MaxUint16 {
		return fmt.Errorf("cols must be between %d and %d, got %d", xterm.MinimumCols, math.MaxUint16, cols)
	}
	if rows < xterm.MinimumRows || rows > math.MaxUint16 {
		return fmt.Errorf("rows must be between %d and %d, got %d", xterm.MinimumRows, math.MaxUint16, rows)
	}
	return nil
}
