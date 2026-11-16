//go:build !windows

package isolation

import (
	"fmt"
	"os"
	"strings"
)

// dockerUserSpec keeps container writes owned by the host user that runs
// PicoClaw. A configured value is used verbatim.
func dockerUserSpec(configured string) string {
	if value := strings.TrimSpace(configured); value != "" {
		return value
	}
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}
