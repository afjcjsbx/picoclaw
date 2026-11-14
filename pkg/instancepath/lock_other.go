//go:build !unix && !windows

package instancepath

import (
	"fmt"
	"os"
)

func tryLock(*os.File) error {
	return fmt.Errorf("runtime directory locking is unsupported on this platform")
}

func openLock(path string, create bool) (*os.File, error) {
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	return os.OpenFile(path, flags, 0o600)
}
