package session

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/remorses/tuistory/internal/process"
	"golang.org/x/sys/unix"
)

const shutdownPollInterval = 20 * time.Millisecond

// CloseAndWait closes the session and waits for process-group escalation,
// root-process reaping, and terminal I/O cleanup. Canceling ctx stops only this
// caller's wait; the session continues its owned shutdown to completion.
func (s *Session) CloseAndWait(ctx context.Context, reason string) error {
	s.Close(reason)
	select {
	case <-s.closeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) finishClose(cmd *exec.Cmd, termcastSuffix, cwd string) {
	defer close(s.closeDone)
	if cmd != nil && cmd.Process != nil {
		s.terminateProcessGroups(cmd.Process.Pid)
		<-s.processDone
	}
	<-s.readDone
	<-s.terminalReplyDone
	if termcastSuffix != "" {
		bundleDir := filepath.Join(cwd, ".termcast-bundle")
		for _, suffix := range []string{".db", ".db-shm", ".db-wal"} {
			_ = os.Remove(filepath.Join(bundleDir, "data-"+termcastSuffix+suffix)) // Best-effort generated database cleanup.
		}
	}
}

func (s *Session) terminateProcessGroups(pid int) {
	process.KillSessionGroups(pid, unix.SIGTERM)
	timer := time.NewTimer(killGraceDuration)
	defer timer.Stop()
	ticker := time.NewTicker(shutdownPollInterval)
	defer ticker.Stop()
	for {
		groups, err := process.Groups(pid)
		if err == nil && len(groups) == 0 {
			return
		}
		if err != nil {
			slog.Error("checking session shutdown", "session", pid, "error", err)
		}
		select {
		case <-timer.C:
			// Root exit does not imply group exit: descendants can retain the
			// session ID after their original parent has been reaped.
			process.KillSessionGroups(pid, unix.SIGKILL)
			return
		case <-ticker.C:
		}
	}
}
