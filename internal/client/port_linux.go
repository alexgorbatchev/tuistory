package client

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func findPIDsListeningOnEndpoint(endpoint netip.AddrPort) ([]int, error) {
	if !endpoint.IsValid() || endpoint.Port() == 0 || endpoint.Addr().Zone() != "" {
		return nil, fmt.Errorf("invalid endpoint %s", endpoint)
	}
	name := "tcp6"
	if endpoint.Addr().Is4() {
		name = "tcp"
	}
	// proc prints network-order addresses as native uint32 words on both
	// IPv4 and IPv6; keep that family and every address word in the match.
	var address strings.Builder
	bytes := endpoint.Addr().AsSlice()
	for i := 0; i < len(bytes); i += 4 {
		fmt.Fprintf(&address, "%08X", binary.NativeEndian.Uint32(bytes[i:i+4]))
	}
	want := fmt.Sprintf("%s:%04X", address.String(), endpoint.Port())
	inodes := make(map[string]bool)
	data, err := os.ReadFile("/proc/net/" + name)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		if fields[1] == want {
			inodes["socket:["+fields[9]+"]"] = true
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
