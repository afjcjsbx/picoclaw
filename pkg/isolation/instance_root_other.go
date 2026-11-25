//go:build !linux

package isolation

import "github.com/sipeed/picoclaw/pkg/config"

// defaultLinuxInstanceExposePaths is a Linux-only function; this stub allows
// the package to compile on other platforms. It is never called at runtime.
func defaultLinuxInstanceExposePaths(_ string) []config.ExposePath {
	return nil
}
