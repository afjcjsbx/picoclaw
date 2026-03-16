package tree

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// MigrateFromLinear migrates legacy flat session data (from memory.Store or
// session.SessionManager) into the tree format. It reads all .jsonl and .json
// files from the legacy session directory and creates corresponding tree session
// files. Returns the number of sessions migrated.
func MigrateFromLinear(ctx context.Context, legacyDir, treeDir string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := os.Stat(legacyDir); os.IsNotExist(err) {
		return 0, nil
	}

	files, err := os.ReadDir(legacyDir)
	if err != nil {
		return 0, fmt.Errorf("tree migration: read dir: %w", err)
	}

	os.MkdirAll(treeDir, 0o755)
	migrated := 0

	for _, f := range files {
		if f.IsDir() {
			continue
		}

		select {
		case <-ctx.Done():
			return migrated, ctx.Err()
		default:
		}

		name := f.Name()

		// Skip already-migrated markers
		if strings.HasSuffix(name, ".tree-migrated") {
			continue
		}
		// Skip metadata files
		if strings.HasSuffix(name, ".meta.json") {
			continue
		}

		var msgs []providers.Message
		var summary string
		var sessionKey string
		srcPath := filepath.Join(legacyDir, name)

		switch {
		case strings.HasSuffix(name, ".jsonl"):
			// Legacy JSONL format (from memory.JSONLStore)
			sessionKey = strings.TrimSuffix(name, ".jsonl")
			m, s, err := readLegacyJSONL(srcPath, legacyDir, sessionKey)
			if err != nil {
				log.Printf("tree migration: skip %s: %v", name, err)
				continue
			}
			msgs = m
			summary = s

		case strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".migrated"):
			// Legacy JSON format (from session.SessionManager)
			sessionKey = strings.TrimSuffix(name, ".json")
			m, s, err := readLegacyJSON(srcPath)
			if err != nil {
				log.Printf("tree migration: skip %s: %v", name, err)
				continue
			}
			msgs = m
			summary = s

		default:
			continue
		}

		if len(msgs) == 0 {
			continue
		}

		// Create a tree session from the linear messages
		sm := Create(sessionKey, treeDir)

		// If there's a summary, add it as a compaction entry first
		if summary != "" {
			var firstEntryID string
			for i, msg := range msgs {
				id := sm.AppendProviderMessage(msg)
				if i == 0 {
					firstEntryID = id
				}
			}
			if firstEntryID != "" {
				sm.AppendCompaction(summary, firstEntryID, 0, nil, false)
			}
		} else {
			for _, msg := range msgs {
				sm.AppendProviderMessage(msg)
			}
		}

		// Mark the legacy file as migrated
		if err := os.Rename(srcPath, srcPath+".tree-migrated"); err != nil {
			log.Printf("tree migration: rename %s: %v", name, err)
		}

		migrated++
	}

	return migrated, nil
}

// readLegacyJSONL reads messages from a legacy .jsonl file and optional .meta.json.
func readLegacyJSONL(jsonlPath, dir, key string) ([]providers.Message, string, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	skip := 0
	summary := ""

	// Try reading meta
	metaPath := filepath.Join(dir, sanitizeKey(key)+".meta.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var meta struct {
			Summary string `json:"summary"`
			Skip    int    `json:"skip"`
		}
		if json.Unmarshal(data, &meta) == nil {
			skip = meta.Skip
			summary = meta.Summary
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var msgs []providers.Message
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNum++
		if lineNum <= skip {
			continue
		}
		var msg providers.Message
		if json.Unmarshal(line, &msg) == nil {
			msgs = append(msgs, msg)
		}
	}

	return msgs, summary, scanner.Err()
}

// readLegacyJSON reads a legacy JSON session file.
func readLegacyJSON(path string) ([]providers.Message, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}

	var sess struct {
		Key      string              `json:"key"`
		Messages []providers.Message `json:"messages"`
		Summary  string              `json:"summary"`
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, "", err
	}

	return sess.Messages, sess.Summary, nil
}

// sanitizeKey mirrors the sanitization used by the legacy JSONL store.
func sanitizeKey(key string) string {
	s := strings.ReplaceAll(key, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}
