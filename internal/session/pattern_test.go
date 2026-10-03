package session

import (
	"strings"
	"testing"
	"time"
)

func TestWaitForTextPatternFlags(t *testing.T) {
	for _, tt := range []struct {
		name, pattern, content string
	}{
		{"case insensitive", "/BOTTOM/i", "top\r\nbottom"},
		{"multiline anchors", "/^bottom$/m", "top\r\nbottom"},
		{"dot matches newline", "/top.*bottom/s", "top\r\nbottom"},
		{"combined flags", "/^TOP.*BOTTOM$/ims", "top\r\nbottom"},
		{"global", "/bottom/g", "top\r\nbottom"},
		{"global combined", "/^BOTTOM$/gim", "top\r\nbottom"},
		{"literal punctuation", "target.*[0]", "target.*[0]"},
		{"literal slash prefix", "/target", "/target"},
		{"escaped slash", `/a\/b/`, "a/b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := terminalSession(t, 40, 3, tt.content)
			for range 2 {
				if _, err := s.WaitForText(tt.pattern, 80*time.Millisecond); err != nil {
					t.Fatalf("WaitForText(%q) did not match %q: %v", tt.pattern, tt.content, err)
				}
			}
		})
	}
}

func TestPatternErrorsBeforeWaitOrClickInput(t *testing.T) {
	for _, tt := range []struct {
		name, pattern, want string
	}{
		{"malformed expression", "/[broken/", "invalid regex"},
		{"duplicate flag", "/target/ii", "duplicate regex flag"},
		{"unknown flag", "/target/z", "unsupported regex flag"},
		{"Unicode flag", "/target/u", "unsupported regex flag"},
		{"sticky flag", "/target/y", "unsupported regex flag"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := inputEchoSession(t)
			if err := s.WriteRaw(tt.pattern); err != nil {
				t.Fatal(err)
			}
			if _, err := s.WaitForText("ready", time.Second); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(s.ReadAll(), tt.pattern) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(s.ReadAll(), tt.pattern) {
				t.Fatalf("literal test pattern was not echoed: %q", s.ReadAll())
			}
			s.Read()
			if _, err := s.WaitForText(tt.pattern, 80*time.Millisecond); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("WaitForText(%q) = %v, want %q", tt.pattern, err, tt.want)
			}
			if err := s.Click(tt.pattern, true, 80*time.Millisecond); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Click(%q) = %v, want %q", tt.pattern, err, tt.want)
			}
			if s.WaitForUnreadOutput(50 * time.Millisecond) {
				t.Errorf("invalid pattern sent mouse input: %q", s.Read())
			}
		})
	}
}

func TestClickPatternFlags(t *testing.T) {
	for _, tt := range []struct {
		name, pattern, content string
	}{
		{"case insensitive", "/TARGET/i", "target"},
		{"multiline", "/^TARGET$/im", "target"},
		{"dotall", "/tar.et/s", "target"},
		{"global", "/TARGET/gi", "target"},
		{"literal", "target.*[0]", "target.*[0]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := inputEchoSession(t)
			if err := s.WriteRaw("\r\n" + tt.content); err != nil {
				t.Fatal(err)
			}
			if _, err := s.WaitForText(tt.content, time.Second); err != nil {
				t.Fatal(err)
			}
			if err := s.Click(tt.pattern, false, time.Second); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for !strings.Contains(s.GetRawOutput(), "\x1b[<0;1;2M") && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !strings.Contains(s.GetRawOutput(), "\x1b[<0;1;2M") {
				t.Fatalf("wrong click coordinates: %q", s.GetRawOutput())
			}
		})
	}
}
