package isolation

import (
	"os"
	"runtime"
	"strings"
)

// IsParentEnvAllowed reports whether key is in the base safe set that every
// child process should inherit from the parent. It is intentionally narrow:
// it covers runtime necessities (PATH, locale, temp dir, XDG dirs) but
// excludes any credential variables such as API keys.
//
// MCP stdio servers call this directly instead of duplicating the predicate.
func IsParentEnvAllowed(key string) bool {
	switch key {
	case "PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "SHELL", "TMPDIR":
		return true
	}
	if strings.HasPrefix(key, "XDG_") {
		return true
	}
	if runtime.GOOS == "windows" {
		switch strings.ToUpper(key) {
		case "PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "SHELL", "TMPDIR",
			"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP",
			"USERPROFILE", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "HOMEDRIVE", "HOMEPATH":
			return true
		}
	}
	return false
}

// InheritedEnv returns the slice of environment entries that a child process
// should start from, depending on whether isolation is enabled:
//
//   - Isolation disabled: returns os.Environ() unchanged. The existing
//     per-site behavior (full parent inheritance) is preserved exactly.
//   - Isolation enabled: returns only entries whose key passes
//     IsParentEnvAllowed or matches one of the extra patterns. Trailing *
//     on a pattern enables prefix matching (vendor-owned namespaces only);
//     a pattern without * is an exact name match.
//
// Call sites that need additional variables pass them in extra, for example:
//
//	isolation.InheritedEnv("ANTHROPIC_*", "HTTP_PROXY", "HTTPS_PROXY")
//
// The returned slice is a fresh copy. Callers may append to it freely.
func InheritedEnv(extra ...string) []string {
	if !CurrentConfig().Enabled {
		return os.Environ()
	}
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, entry := range src {
		idx := strings.IndexByte(entry, '=')
		if idx <= 0 {
			// Skip entries with no key (malformed) or empty key.
			continue
		}
		key := entry[:idx]
		if IsParentEnvAllowed(key) || isExtraAllowed(key, extra) {
			out = append(out, entry)
		}
	}
	return out
}

// isExtraAllowed reports whether key is covered by one of the extra patterns.
// A trailing * makes it a prefix match; otherwise it is an exact match.
func isExtraAllowed(key string, extra []string) bool {
	for _, pattern := range extra {
		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(key, pattern[:len(pattern)-1]) {
				return true
			}
		} else if key == pattern {
			return true
		}
	}
	return false
}
