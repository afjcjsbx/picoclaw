//go:build freebsd

package seahorse

import "syscall"

func diskFreeBytes(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return st.Bavail * int64(st.Bsize), true
}
