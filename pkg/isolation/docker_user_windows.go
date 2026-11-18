//go:build windows

package isolation

import "strings"

// dockerUserSpec uses Docker's default user on Windows unless the user
// explicitly configured one.
func dockerUserSpec(configured string) string {
	return strings.TrimSpace(configured)
}
