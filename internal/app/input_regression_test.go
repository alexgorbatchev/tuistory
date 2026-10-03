package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/remorses/tuistory/internal/relay"
	"github.com/remorses/tuistory/internal/session"
)

func TestCaptureFramesIntervalValidation(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("overflowing millisecond interval cannot fit a 32-bit CLI integer")
	}
	const maxInterval = math.MaxInt64 / int64(time.Millisecond)
	for _, tt := range []struct {
		name     string
		interval int64
		valid    bool
	}{
		{"negative", -1, false},
		{"overflow", maxInterval + 1, false},
		{"overflow wraps positive", 18_446_744_073_710, false},
		{"zero", 0, true},
		{"maximum duration", maxInterval, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg, s := inputCommandSession(t)
			res := ExecuteCommand([]string{"capture-frames", "z", "-s", "input", "--count", "1", "--interval", strconv.FormatInt(tt.interval, 10)}, reg, ".", nil)
			if tt.valid {
				var frames []string
				if err := json.Unmarshal([]byte(res.Stdout), &frames); err != nil || res.ExitCode != 0 || len(frames) != 1 {
					t.Fatalf("valid capture: %+v, frames %q, JSON error %v", res, frames, err)
				}
				if _, err := s.WaitForText("z", time.Second); err != nil {
					t.Fatal(err)
				}
				return
			}
			want := fmt.Sprintf("interval must be between 0 and %d milliseconds", maxInterval)
			if res.ExitCode == 0 || !strings.Contains(res.Stderr, want) || res.Stdout != "" {
				t.Errorf("invalid interval accepted or misreported: %+v; want %q", res, want)
			}
			if s.WaitForUnreadOutput(50*time.Millisecond) || s.ReadAll() != "ready" {
				t.Errorf("invalid interval sent input: %q", s.ReadAll())
			}
		})
	}
}

func TestInputCommandsRejectInvalidOptions(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"negative count", []string{"capture-frames", "z", "--count", "-1"}, "count must be positive"},
		{"zero count", []string{"capture-frames", "z", "--count", "0"}, "count must be positive"},
		{"negative columns", []string{"resize", "--", "-1", "3"}, "cols must be between 2 and 65535"},
		{"clamped columns", []string{"resize", "1", "3"}, "cols must be between 2 and 65535"},
		{"overflow rows", []string{"resize", "40", "65536"}, "rows must be between 1 and 65535"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reg, s := inputCommandSession(t)
			args := append([]string{"-s", "input"}, tt.args...)
			res := ExecuteCommand(args, reg, ".", nil)
			if res.ExitCode == 0 || !strings.Contains(res.Stderr, tt.want) || res.Stdout != "" {
				t.Errorf("invalid command result: %+v; want %q", res, tt.want)
			}
			if s.Cols() != 40 || s.Rows() != 3 {
				t.Errorf("invalid command changed dimensions to %dx%d", s.Cols(), s.Rows())
			}
			if s.WaitForUnreadOutput(50*time.Millisecond) || s.ReadAll() != "ready" {
				t.Errorf("invalid command sent input: %q", s.ReadAll())
			}
		})
	}
}

func TestScrollLineCountValidation(t *testing.T) {
	for _, tt := range []struct {
		name, lines, want string
		count             int
	}{
		{"negative", "-1", "lines must be nonnegative", 0},
		{"nonnumeric", "broken", "invalid scroll lines", 0},
		{"fractional", "1.5", "invalid scroll lines", 0},
		{"zero", "0", "", 0},
		{"positive", "2", "", 2},
		{"default", "", "", 1},
	} {
		for _, direction := range []string{"up", "down"} {
			t.Run(direction+"/"+tt.name, func(t *testing.T) {
				reg, s := inputCommandSession(t)
				args := []string{"-s", "input", "scroll", direction}
				if tt.lines != "" {
					args = append(args, "--", tt.lines)
				}
				res := ExecuteCommand(args, reg, ".", nil)
				if tt.want != "" {
					if res.ExitCode == 0 || !strings.Contains(res.Stderr, tt.want) {
						t.Errorf("invalid scroll accepted or misreported: %+v", res)
					}
				} else if res.ExitCode != 0 || res.Stdout != "OK" {
					t.Errorf("valid scroll failed: %+v", res)
				}
				if tt.count == 0 {
					if s.WaitForUnreadOutput(50*time.Millisecond) || s.ReadAll() != "ready" {
						t.Errorf("zero or invalid count sent input: %q", s.GetRawOutput())
					}
					return
				}
				event := "\x1b[<64;21;2M"
				if direction == "down" {
					event = "\x1b[<65;21;2M"
				}
				deadline := time.Now().Add(time.Second)
				for strings.Count(s.GetRawOutput(), event) < tt.count && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if got := strings.Count(s.GetRawOutput(), event); got != tt.count {
					t.Errorf("scroll sent %d events, want %d: %q", got, tt.count, s.GetRawOutput())
				}
			})
		}
	}
}

func inputCommandSession(t *testing.T) (*relay.SessionRegistry, *session.Session) {
	t.Helper()
	reg := relay.NewSessionRegistry()
	s, err := session.New(session.LaunchOptions{Command: "sh", Args: []string{"-c", "stty raw -echo; printf ready; cat"}, Cols: 40, Rows: 3, IdleDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	reg.Set("input", s)
	t.Cleanup(func() { reg.CloseAll("test") })
	if _, err := s.WaitForText("ready", time.Second); err != nil {
		t.Fatal(err)
	}
	s.Read()
	return reg, s
}
