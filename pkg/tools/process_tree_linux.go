//go:build linux

package tools

import (
	"os"
	"strconv"
	"strings"
)

func descendantPIDs(root int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	parent := make(map[int]int, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		ppid, ok := readProcPPID(pid)
		if ok {
			parent[pid] = ppid
		}
	}
	return descendantsFromParentMap(root, parent)
}

func readProcPPID(pid int) (int, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	// The comm field is parenthesized and may itself contain spaces or
	// parentheses; the state and ppid fields follow the last ')'.
	text := string(data)
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 || closeParen+1 >= len(text) {
		return 0, false
	}
	fields := strings.Fields(text[closeParen+1:])
	// fields[0] is the state, fields[1] is ppid.
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
