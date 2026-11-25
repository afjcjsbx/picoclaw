// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// MemoryStore renders the agent's persistent memory for the system prompt.
//   - Long-term memory: memory/MEMORY.md
//   - User profile: USER.md
//   - Daily notes: memory/YYYYMM/YYYYMMDD.md
//
// Writes go through the memory tool (MEMORY.md, USER.md) or the generic file
// tools (daily notes), so the store only reads. What it returns is always
// bounded by the configured limits; the files themselves are never modified.
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
	userFile   string
	cfg        config.MemoryConfig
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
		userFile:   filepath.Join(workspace, "USER.md"),
		// Limits fall back to the config defaults when left at zero.
		cfg: config.MemoryConfig{Enabled: true, DailyNotesEnabled: true},
	}
}

// dailyNotePath returns the path to the daily note file for t
// (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) dailyNotePath(t time.Time) string {
	dateStr := t.Format("20060102") // YYYYMMDD
	monthDir := dateStr[:6]         // YYYYMM
	return filepath.Join(ms.memoryDir, monthDir, dateStr+".md")
}

// readBounded reads path and fits it to limit characters. It returns "" when
// the file is missing or blank.
func (ms *MemoryStore) readBounded(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(ms.workspace, path)
	if err != nil {
		rel = path
	}
	return fitMemory(string(data), limit, filepath.ToSlash(rel), path)
}

// recentDailyNotes returns the daily notes of the last N days, newest first,
// each bounded to limit characters.
func (ms *MemoryStore) recentDailyNotes(days, limit int) string {
	var notes []string
	for i := range days {
		path := ms.dailyNotePath(time.Now().AddDate(0, 0, -i))
		if note := ms.readBounded(path, limit); note != "" {
			notes = append(notes, note)
		}
	}
	return strings.Join(notes, "\n\n---\n\n")
}

// Snapshot renders the memory block of the system prompt: long-term memory,
// the user profile and recent daily notes, each within its configured limit.
// It returns "" when there is nothing to show.
func (ms *MemoryStore) Snapshot() string {
	var sections []string
	add := func(title, text string) {
		if text != "" {
			sections = append(sections, "## "+title+"\n\n"+text)
		}
	}
	if ms.cfg.Enabled {
		add("Long-term Memory", ms.readBounded(ms.memoryFile, ms.cfg.GetMemoryCharLimit()))
	}
	add("USER.md", ms.readBounded(ms.userFile, ms.cfg.GetUserCharLimit()))
	if ms.cfg.DailyNotesEnabled {
		add(
			"Recent Daily Notes",
			ms.recentDailyNotes(ms.cfg.GetRecentDailyNotesCount(), ms.cfg.GetDailyNotesCharLimit()),
		)
	}
	return strings.Join(sections, "\n\n---\n\n")
}

// fitMemory bounds text to limit characters without touching the file it came
// from. Markdown sections (split at heading lines) are kept whole from the
// newest backwards, which suits files that are appended to; older sections
// shrink to their heading so the model still sees the index. When even the
// newest section does not fit, its newest lines are kept. A footer says what
// was left out and where the full file is. label is the workspace-relative
// path and path the absolute one, used for the read_file hint.
func fitMemory(text string, limit int, label, path string) string {
	text = strings.TrimSpace(text)
	total := utf8.RuneCountInString(text)
	if total == 0 || total <= limit {
		return text
	}

	type section struct {
		head string
		body []string
	}
	secs := []section{{}}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "#") {
			secs = append(secs, section{head: line})
			continue
		}
		last := &secs[len(secs)-1]
		last.body = append(last.body, line)
	}

	budget := limit
	for _, sec := range secs {
		if sec.head != "" {
			budget -= utf8.RuneCountInString(sec.head) + 1
		}
	}
	if budget <= 0 {
		// Headings alone fill the budget: fall back to the newest lines.
		secs, budget = []section{{body: strings.Split(text, "\n")}}, limit
	}

	kept := make([][]string, len(secs))
	keptAny := false
	for i := len(secs) - 1; i >= 0 && budget > 0; i-- {
		body := secs[i].body
		if n := runeCountLines(body); n <= budget {
			kept[i], budget, keptAny = body, budget-n, keptAny || n > 0
			continue
		}
		if !keptAny {
			// The newest section alone is too big: keep its newest lines.
			kept[i] = newestLines(body, budget)
		}
		break
	}

	var sb strings.Builder
	for i, sec := range secs {
		if sec.head != "" {
			sb.WriteString(sec.head + "\n")
		}
		for _, line := range kept[i] {
			sb.WriteString(line + "\n")
		}
	}
	out := strings.TrimSpace(sb.String())
	return fmt.Sprintf(
		"%s\n\n[%s truncated: showing %d of %d chars, older content omitted. Full file: read_file %s]",
		out, label, utf8.RuneCountInString(out), total, path,
	)
}

func runeCountLines(lines []string) int {
	n := 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l) + 1
	}
	return n
}

// newestLines returns the trailing lines that fit in budget characters. A
// single line longer than the budget is cut from its start.
func newestLines(lines []string, budget int) []string {
	n, start := 0, len(lines)
	for start > 0 {
		c := utf8.RuneCountInString(lines[start-1]) + 1
		if n+c > budget {
			break
		}
		n, start = n+c, start-1
	}
	if start == len(lines) && len(lines) > 0 {
		r := []rune(lines[len(lines)-1])
		return []string{string(r[max(0, len(r)-budget):])}
	}
	return lines[start:]
}
