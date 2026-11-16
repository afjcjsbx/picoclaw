// Package plog is a deliberately tiny stderr logger for the evolution plugin.
// The hook protocol owns stdout, so every diagnostic must go to stderr.
package plog

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	debugEnabled atomic.Bool
	out          io.Writer = os.Stderr
	mu           sync.Mutex
)

// SetDebug enables or disables debug-level messages.
func SetDebug(enabled bool) {
	debugEnabled.Store(enabled)
}

// SetOutput redirects log output, primarily for tests.
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	out = w
}

func logLine(level, component, message string) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintf(out, "level=%s component=%s msg=%q\n", level, component, message)
}

func logFields(level, component, message string, fields map[string]any) {
	if level == "DEBUG" && !debugEnabled.Load() {
		return
	}
	var b strings.Builder
	b.WriteString(message)
	if len(fields) > 0 {
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, " %s=%v", key, fields[key])
		}
	}
	logLine(level, component, b.String())
}

func DebugCF(component, message string, fields map[string]any) {
	logFields("DEBUG", component, message, fields)
}

func InfoCF(component, message string, fields map[string]any) {
	logFields("INFO", component, message, fields)
}

func WarnCF(component, message string, fields map[string]any) {
	logFields("WARN", component, message, fields)
}

func ErrorCF(component, message string, fields map[string]any) {
	logFields("ERROR", component, message, fields)
}
