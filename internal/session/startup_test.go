package session

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDirectChildOutputSurvivesImmediateExit(t *testing.T) {
	for i := range 200 {
		want := fmt.Sprintf("immediate-%d", i)
		s, err := New(LaunchOptions{Command: "echo", Args: []string{want}})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WaitForData(time.Second); err != nil {
			s.Close("test")
			t.Fatalf("child %d output lost: %v", i, err)
		}
		text, err := s.WaitForText(want, time.Second)
		s.Close("test")
		if err != nil || !strings.Contains(text, want) {
			t.Fatalf("child %d output %q: %v", i, text, err)
		}
		if !s.WaitForExit(time.Second) {
			t.Fatalf("child %d did not exit", i)
		}
	}
}
