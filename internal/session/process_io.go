package session

import (
	"fmt"
	"os"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// A nonblocking descriptor passed to NewFile uses Go's runtime poller. This
// makes closing the master interrupt both reads and writes, including on Darwin
// where pty.Open returns an unpollable wrapper around a blocking descriptor.
func openPTY() (*os.File, *os.File, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, nil, err
	}
	conn, err := master.SyscallConn()
	fd := -1
	if err == nil {
		var controlErr error
		err = conn.Control(func(raw uintptr) {
			fd, controlErr = unix.FcntlInt(raw, unix.F_DUPFD_CLOEXEC, 0)
			if controlErr == nil {
				controlErr = syscall.SetNonblock(fd, true)
			}
		})
		if err == nil {
			err = controlErr
		}
	}
	closeErr := master.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		if fd >= 0 {
			_ = unix.Close(fd) // Best-effort cleanup after setup failure.
		}
		_ = slave.Close() // Best-effort cleanup after setup failure.
		return nil, nil, fmt.Errorf("preparing pollable pty: %w", err)
	}
	return os.NewFile(uintptr(fd), master.Name()), slave, nil
}
