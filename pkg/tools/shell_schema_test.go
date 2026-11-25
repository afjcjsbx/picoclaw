package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestForegroundCapWordingAgrees keeps the model-facing text that mentions the
// foreground cap in step: the tool description, the timeout parameter and the
// note returned when a run is promoted must all quote the same number.
func TestForegroundCapWordingAgrees(t *testing.T) {
	capSeconds := fmt.Sprintf("%ds", int64(maxForegroundTimeout/time.Second))

	tool := &ExecTool{foregroundCap: maxForegroundTimeout}
	timeoutDescription, _ := tool.Parameters()["properties"].(map[string]any)["timeout"].(map[string]any)["description"].(string)

	for name, text := range map[string]string{
		"description":        execDescriptionFor("linux"),
		"timeout parameter":  timeoutDescription,
		"promotion (above)":  tool.foregroundPromotionNote(false, maxForegroundTimeout+time.Second),
		"promotion (no cap)": tool.foregroundPromotionNote(true, 0),
	} {
		require.True(t, strings.Contains(text, capSeconds), "%s does not mention %s: %q", name, capSeconds, text)
	}
}
