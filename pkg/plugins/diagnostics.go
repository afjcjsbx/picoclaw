package plugins

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// stderrLogger bounds each logged chunk; stdout remains exclusively MCP data.
type stderrLogger struct{ plugin, server string }

func (w stderrLogger) Write(data []byte) (int, error) {
	message := data
	if len(message) > 4096 {
		message = message[:4096]
	}
	if text := strings.TrimSpace(string(message)); text != "" {
		logger.DebugCF("plugins", "MCP stderr", map[string]any{"plugin": w.plugin, "server": w.server, "message": text})
	}
	return len(data), nil
}
