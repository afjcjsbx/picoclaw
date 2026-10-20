package hardwaretools

import (
	"fmt"
	"strings"
)

// unknownActionResult reports an action the tool does not implement, listing
// the actions it accepts.
func unknownActionResult(action string, valid ...string) *ToolResult {
	return ErrorResult(fmt.Sprintf("unknown action: %s (valid: %s)", action, strings.Join(valid, ", ")))
}
