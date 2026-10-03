package session

import (
	"strings"
	"testing"
)

func TestLaunchRejectsInvalidDimensionsBeforeStarting(t *testing.T) {
	cases := []struct {
		name string
		cols int
		rows int
		want string
	}{
		{name: "negative columns", cols: -1, rows: 10, want: "cols must be between"},
		{name: "emulator column minimum", cols: 1, rows: 10, want: "cols must be between"},
		{name: "column overflow", cols: 65536, rows: 10, want: "cols must be between"},
		{name: "negative rows", cols: 40, rows: -1, want: "rows must be between"},
		{name: "row overflow", cols: 40, rows: 65536, want: "rows must be between"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(LaunchOptions{Command: "echo", Args: []string{"must not start"}, Cols: tc.cols, Rows: tc.rows})
			if err == nil {
				s.Close("test")
				t.Fatal("invalid dimensions started a child")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("launch error = %v, want %q", err, tc.want)
			}
		})
	}
}
