package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func findPIDsListeningOnPort(port int) ([]int, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	inodes := make(map[string]bool)
	for _, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile("/proc/net/" + name)
		if errors.Is(err, os.ErrNotExist) && name == "tcp6" {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, rawPort, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			value, err := strconv.ParseUint(rawPort, 16, 16)
			if err == nil && int(value) == port {
				inodes["socket:["+fields[9]+"]"] = true
			}
		}
	}
	if len(inodes) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		} // Processes may exit or belong to another user.
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err == nil && inodes[link] {
				pids = append(pids, pid)
				break
			}
		}
	}
	slices.Sort(pids)
	return pids, nil
}
