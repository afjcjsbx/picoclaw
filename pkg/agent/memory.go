// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// MemoryStore provides read access to the agent's persistent memory.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
//
// Writes go through the agent's generic file tools (write_file / edit_file),
// which target these paths directly, so the store only reads.
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
}

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure the memory directory exists. Use 0o700: memory may contain
	// personal notes, so keep it owner-only rather than world-listable.
	if err := os.MkdirAll(memoryDir, 0o700); err != nil {
		logger.WarnCF("agent", "Failed to create memory directory", map[string]any{
			"dir":   memoryDir,
			"error": err.Error(),
		})
	}

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

// recentDailyNotesDays is the number of daily notes folded into the memory
// context and, therefore, into the cached system prompt.
const recentDailyNotesDays = 3

// dailyNotePath returns the path to the daily note file for t
// (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) dailyNotePath(t time.Time) string {
	dateStr := t.Format("20060102") // YYYYMMDD
	monthDir := dateStr[:6]         // YYYYMM
	return filepath.Join(ms.memoryDir, monthDir, dateStr+".md")
}

// RecentDailyNotePaths returns the paths of the last N daily notes, including
// entries for files that do not exist yet. It reports exactly the set of files
// GetRecentDailyNotes reads, so callers can track them for cache invalidation.
func (ms *MemoryStore) RecentDailyNotePaths(days int) []string {
	if days <= 0 {
		return nil
	}
	now := time.Now()
	paths := make([]string, 0, days)
	for i := range days {
		paths = append(paths, ms.dailyNotePath(now.AddDate(0, 0, -i)))
	}
	return paths
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	if data, err := os.ReadFile(ms.memoryFile); err == nil {
		return string(data)
	}
	return ""
}

// GetRecentDailyNotes returns daily notes from the last N days, newest first.
// Existing notes are joined with a "\n\n---\n\n" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var sb strings.Builder
	first := true

	for i := range days {
		filePath := ms.dailyNotePath(time.Now().AddDate(0, 0, -i))

		if data, err := os.ReadFile(filePath); err == nil {
			if !first {
				sb.WriteString("\n\n---\n\n")
			}
			sb.Write(data)
			first = false
		}
	}

	return sb.String()
}

// GetMemoryContext returns formatted memory context for the agent prompt.
// Includes long-term memory and recent daily notes.
func (ms *MemoryStore) GetMemoryContext() string {
	longTerm := ms.ReadLongTerm()
	recentNotes := ms.GetRecentDailyNotes(recentDailyNotesDays)

	if longTerm == "" && recentNotes == "" {
		return ""
	}

	var sb strings.Builder

	if longTerm != "" {
		sb.WriteString("## Long-term Memory\n\n")
		sb.WriteString(longTerm)
	}

	if recentNotes != "" {
		if longTerm != "" {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Recent Daily Notes\n\n")
		sb.WriteString(recentNotes)
	}

	return sb.String()
}
