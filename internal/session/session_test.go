package session

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSessionEcho(t *testing.T) {
	s, err := New(LaunchOptions{
		Command: "echo",
		Args:    []string{"hello world"},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close("test-done")

	if err := s.WaitForData(5 * time.Second); err != nil {
		t.Fatalf("WaitForData error: %v", err)
	}

	txt, err := s.Text(TextOptions{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("Text error: %v", err)
	}

	if !strings.Contains(txt, "hello world") {
		t.Fatalf("expected text to contain 'hello world', got: %q", txt)
	}
}

func TestSessionTypeAndPress(t *testing.T) {
	s, err := New(LaunchOptions{
		Command: "cat",
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close("test-done")

	if err := s.Type("hello"); err != nil {
		t.Fatalf("Type error: %v", err)
	}

	if err := s.Press([]string{"enter"}); err != nil {
		t.Fatalf("Press error: %v", err)
	}

	matched, err := s.WaitForText("hello", 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForText error: %v", err)
	}
	if !strings.Contains(matched, "hello") {
		t.Fatalf("expected matched output to contain 'hello', got: %q", matched)
	}
}

func TestSessionReadAndReadAll(t *testing.T) {
	s, err := New(LaunchOptions{
		Command: "sh",
		Args:    []string{"-c", "echo line-one && sleep 0.2 && echo line-two"},
		Cols:    60,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close("test-done")

	_, err = s.WaitForText("line-one", 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForText error: %v", err)
	}

	firstRead := s.Read()
	if !strings.Contains(firstRead, "line-one") {
		t.Fatalf("expected firstRead to contain 'line-one', got: %q", firstRead)
	}

	// Wait for second line
	_, err = s.WaitForText("line-two", 5*time.Second)
	if err != nil {
		t.Fatalf("WaitForText error: %v", err)
	}

	secondRead := s.Read()
	if !strings.Contains(secondRead, "line-two") {
		t.Fatalf("expected secondRead to contain 'line-two', got: %q", secondRead)
	}
	if strings.Contains(secondRead, "line-one") {
		t.Fatalf("expected secondRead NOT to contain 'line-one' (already read), got: %q", secondRead)
	}

	all := s.ReadAll()
	if !strings.Contains(all, "line-one") || !strings.Contains(all, "line-two") {
		t.Fatalf("expected ReadAll to contain both lines, got: %q", all)
	}
}

func TestSessionWaitForTextExitsEarlyOnCrash(t *testing.T) {
	s, err := New(LaunchOptions{
		Command: "sh",
		Args:    []string{"-c", "echo crashing-now && exit 7"},
		Cols:    60,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer s.Close("test-done")

	_, err = s.WaitForText("never-matches", 10*time.Second)
	if err == nil {
		t.Fatalf("expected error on crash, got nil")
	}
	if !strings.Contains(err.Error(), "code 7") {
		t.Fatalf("expected error to mention 'code 7', got: %v", err)
	}
	if !strings.Contains(err.Error(), "crashing-now") {
		t.Fatalf("expected error to include last output 'crashing-now', got: %v", err)
	}
}

func TestSessionKillProcessGroup(t *testing.T) {
	// Spawns a grandchild process that would normally be orphaned
	s, err := New(LaunchOptions{
		Command: "sh",
		Args:    []string{"-c", "trap '' HUP TERM; sleep 60 & echo GCPID=$!; wait"},
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	out, err := s.WaitForText("GCPID=", 5*time.Second)
	if err != nil {
		s.Close("cleanup")
		t.Fatalf("WaitForText error: %v", err)
	}

	var grandchildPid int
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, "GCPID="); idx != -1 {
			valStr := strings.TrimSpace(line[idx+6:])
			for _, f := range strings.Fields(valStr) {
				var parsed int
				for _, c := range f {
					if c >= '0' && c <= '9' {
						parsed = parsed*10 + int(c-'0')
					} else {
						break
					}
				}
				if parsed > 0 {
					grandchildPid = parsed
					break
				}
			}
		}
	}

	if grandchildPid == 0 {
		s.Close("cleanup")
		t.Fatalf("failed to parse GCPID from: %q", out)
	}

	// Verify grandchild is alive before close
	p, err := os.FindProcess(grandchildPid)
	if err != nil {
		s.Close("cleanup")
		t.Fatalf("FindProcess error: %v", err)
	}

	s.Close("test-done")

	// Wait for process group kill to propagate
	alive := true
	for i := 0; i < 40; i++ {
		time.Sleep(100 * time.Millisecond)
		err := p.Signal(os.Signal(syscallSignal(0)))
		if err != nil {
			alive = false
			break
		}
	}

	if alive {
		t.Fatalf("grandchild process %d still alive after session.Close()", grandchildPid)
	}
}

func syscallSignal(n int) os.Signal {
	return os.Signal(syscallSig(n))
}

type syscallSig int

func (s syscallSig) String() string { return "signal" }
func (s syscallSig) Signal()        {}
