package process

import "golang.org/x/sys/unix"

// PIDs returns the current kernel process table, subject to caller visibility.
func PIDs() ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(procs))
	for _, proc := range procs {
		if pid := int(proc.Proc.P_pid); pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
