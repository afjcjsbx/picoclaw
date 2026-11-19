//go:build linux

package isolation

import "os"

// linuxEUID returns the effective uid of the current process. It is a variable
// so tests can inject a fixed value and verify root-vs-non-root behavior
// without actually running as root.
var linuxEUID = os.Geteuid
