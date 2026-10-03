package process

import (
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sys/unix"
)

// Groups returns all process group IDs belonging to sessionID.
func Groups(sessionID int) ([]int, error) {
	if sessionID <= 0 {
		return nil, fmt.Errorf("invalid session ID %d", sessionID)
	}

	pids, err := PIDs()
	if err != nil {
		return nil, fmt.Errorf("reading process table: %w", err)
	}

	groups := make(map[int]struct{})
	for _, pid := range pids {
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
