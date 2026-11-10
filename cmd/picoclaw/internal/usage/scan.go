package usage

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
	usagepkg "github.com/sipeed/picoclaw/pkg/usage"
)

// sessionMeta is the subset of a session metadata file needed by the report.
type sessionMeta struct {
	Key     string   `json:"key"`
	Skip    int      `json:"skip"`
	Aliases []string `json:"aliases,omitempty"`
}

// usageRecord is the minimal slice of a persisted message needed for usage.
// Decoding into this shape skips message content, tool calls, and attachments,
// so a single large message never stays in memory.
type usageRecord struct {
	ModelName string              `json:"model_name"`
	Usage     providers.UsageInfo `json:"usage"`
}

type sessionUsage struct {
	Key     string
	Aliases []string
	Summary usagepkg.Summary
}

// scanSessions streams the sessions stored in dir. Only per-session totals are
// retained; message bodies are decoded, reduced to usage, and discarded.
//
// When only is set, sessions that do not match the canonical key or an alias
// are skipped without opening their JSONL file.
func scanSessions(dir, only string, withModels bool, visit func(sessionUsage)) error {
	dirFile, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dirFile.Close()

	only = strings.TrimSpace(only)
	for {
		entries, readErr := dirFile.ReadDir(64)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
				continue
			}

			metaPath := filepath.Join(dir, entry.Name())
			raw, metaErr := os.ReadFile(metaPath)
			if metaErr != nil {
				log.Printf("usage: skipping metadata %s: %v", entry.Name(), metaErr)
				continue
			}
			var meta sessionMeta
			if json.Unmarshal(raw, &meta) != nil || meta.Key == "" {
				continue
			}
			if only != "" && meta.Key != only && !containsAlias(meta.Aliases, only) {
				continue
			}

			var summary usagepkg.Summary
			jsonlPath := strings.TrimSuffix(metaPath, ".meta.json") + ".jsonl"
			if scanErr := scanSessionFile(jsonlPath, meta.Skip, withModels, &summary); scanErr != nil {
				if only != "" {
					return scanErr
				}
				log.Printf("usage: skipping session %q: %v", meta.Key, scanErr)
				continue
			}

			visit(sessionUsage{Key: meta.Key, Aliases: meta.Aliases, Summary: summary})
			if only != "" {
				return nil
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// scanSessionFile decodes consecutive JSON values from one JSONL file. The
// decoder keeps memory bounded per record and does not require a line buffer
// large enough for the biggest tool result.
func scanSessionFile(path string, skip int, withModels bool, summary *usagepkg.Summary) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(bufio.NewReaderSize(file, 16*1024))
	seen := 0
	for {
		var record usageRecord
		if decodeErr := decoder.Decode(&record); decodeErr != nil {
			if decodeErr == io.EOF {
				return nil
			}
			// A crash can leave a partial trailing record. Keep the records
			// decoded so far instead of discarding the whole session.
			return nil
		}
		seen++
		if seen <= skip {
			continue
		}
		if withModels {
			summary.Add(record.ModelName, record.Usage)
		} else {
			summary.Totals.Add(record.Usage)
		}
	}
}

func containsAlias(aliases []string, key string) bool {
	for _, alias := range aliases {
		if alias == key {
			return true
		}
	}
	return false
}
