package process

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"

	"golang.org/x/sys/unix"
)

// Groups returns all process group IDs belonging to sessionID on Linux.
func Groups(sessionID int) ([]int, error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("invalid session ID %d", sessionID)
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("reading /proc: %w", err)
	}

	groups := make(map[int]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}

		sid, err := unix.Getsid(pid)
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			continue
		}
		if sid != sessionID {
			continue
		}

		pgid, err := unix.Getpgid(pid)
		if errors.Is(err, unix.ESRCH) {
			continue
		}
		if err != nil {
			continue
		}

		// Recheck sid in case pid was recycled
		sidCheck, err := unix.Getsid(pid)
		if err == nil && sidCheck == sessionID && pgid > 0 {
			groups[pgid] = struct{}{}
		}
	}

	result := make([]int, 0, len(groups))
	for g := range groups {
		result = append(result, g)
	}
	slices.Sort(result)
	return result, nil
}

// KillSessionGroups signals all process groups belonging to sessionID.
func KillSessionGroups(sessionID int, sig unix.Signal) {
	groups, err := Groups(sessionID)
	if err != nil || len(groups) == 0 {
		_ = unix.Kill(-sessionID, sig)
		return
	}

	for _, pgid := range groups {
		_ = unix.Kill(-pgid, sig)
	}
}
