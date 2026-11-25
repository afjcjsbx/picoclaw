//go:build !windows

package dashboardsetup

import "github.com/sipeed/picoclaw/pkg/fileutil"

func writeTokenFile(path string, data []byte) error {
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
