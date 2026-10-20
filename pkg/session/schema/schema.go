// Package schema holds the storage primitives shared by the session stores
// and their clients: session key sanitization and, later, the on-disk
// session record layout and alias normalization.
package schema

import (
	"path/filepath"
	"strings"
)

// SanitizeKey converts a session key into a cross-platform safe filename
// component. It replaces ':' (session key separator) and '/' and '\' with
// '_' so composite IDs (e.g. Telegram forum "chatID/threadID", Slack
// "channel/thread_ts") do not create subdirectories or break on Windows.
func SanitizeKey(key string) string {
	s := strings.ReplaceAll(key, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// Path returns the path of a session file inside dir. The key is sanitized
// like a filename component, so callers cannot escape dir through the key.
func Path(dir, key, suffix string) string {
	return filepath.Join(dir, SanitizeKey(key)+suffix)
}
