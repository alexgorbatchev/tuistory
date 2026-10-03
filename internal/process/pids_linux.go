package process

import (
	"os"
	"strconv"
)

// PIDs returns the current kernel process table, subject to caller visibility.
func PIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if entry.IsDir() && err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
