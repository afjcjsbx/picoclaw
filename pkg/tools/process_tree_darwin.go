//go:build darwin

package tools

import "golang.org/x/sys/unix"

func descendantPIDs(root int) []int {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}

	parent := make(map[int]int, len(procs))
	for _, proc := range procs {
		parent[int(proc.Proc.P_pid)] = int(proc.Eproc.Ppid)
	}
	return descendantsFromParentMap(root, parent)
}
