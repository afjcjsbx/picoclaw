package utils

import (
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
)

// Global variable to disable truncation
var disableTruncation atomic.Bool

// SetDisableTruncation globally enables or disables string truncation
func SetDisableTruncation(enabled bool) {
	disableTruncation.Store(enabled)
}

// SanitizeMessageContent removes Unicode control characters, format characters (RTL overrides,
// zero-width characters), and other non-graphic characters that could confuse an LLM
// or cause display issues in the agent UI.
func SanitizeMessageContent(input string) string {
	var sb strings.Builder
	// Pre-allocate memory to avoid multiple allocations
	sb.Grow(len(input))

	for _, r := range input {
		// unicode.IsGraphic returns true if the rune is a Unicode graphic character.
		// This includes letters, marks, numbers, punctuation, and symbols.
		// It excludes control characters (Cc), format characters (Cf),
		// surrogates (Cs), and private use (Co).
		if unicode.IsGraphic(r) || r == '\n' || r == '\r' || r == '\t' {
			sb.WriteRune(r)
		}
	}

	return sb.String()
}

// Truncate returns a truncated version of s with at most maxLen runes.
// Handles multi-byte Unicode characters properly.
// If the string is truncated, "..." is appended to indicate truncation.
func Truncate(s string, maxLen int) string {
	// If the no-truncate flag is active, it returns the full string
	if disableTruncation.Load() {
		return s
	}
	if maxLen <= 0 {
		return ""
	}
	// Fast path: the rune count never exceeds the byte count, so a string
	// that fits in maxLen bytes cannot need truncation.
	if len(s) <= maxLen {
		return s
	}

	// Reserve 3 chars for "..."
	keep, suffix := maxLen, ""
	if maxLen > 3 {
		keep, suffix = maxLen-3, "..."
	}

	// Walk rune boundaries directly instead of converting the whole string to
	// []rune: the byte offset of the cut point is only needed once, and long
	// inputs (tool results, message contents) would otherwise pay a 4x-bytes
	// conversion just to count runes.
	count := 0
	cut := len(s)
	for i := range s {
		if count == keep {
			cut = i
		}
		if count == maxLen {
			return s[:cut] + suffix
		}
		count++
	}
	// The string has fewer runes than keep after all: nothing to cut.
	return s
}

// DerefStr dereferences a pointer to a string and
// returns the value or a fallback if the pointer is nil.
func DerefStr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// SortedArgKeys returns the sorted key names of a tool argument map.
// Logging only keys (never values) keeps secrets passed to tools out of log
// files while still making tool calls traceable.
func SortedArgKeys(args map[string]any) []string {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
