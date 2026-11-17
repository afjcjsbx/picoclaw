//go:build !unix

package instancepath

import "os"

// Non-Unix access control is enforced by the OS when files are created.
func checkOwner(os.FileInfo) error { return nil }
