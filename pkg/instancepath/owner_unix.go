//go:build unix

package instancepath

import (
	"fmt"
	"os"
	"syscall"
)

func checkOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Uid) != uint64(os.Geteuid()) {
		return fmt.Errorf("directory is not owned by effective UID %d", os.Geteuid())
	}
	return nil
}
