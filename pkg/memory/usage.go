package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
)

// Usage counters
//
// Each session keeps cumulative provider-reported usage in its metadata. A
// counter grows when a usage-bearing message is appended and is not reduced by
// truncation, compaction, or clearing, so reported spend stays stable while the
// history is rewritten. Promoting an alias moves its counters to the canonical
// session, and deleting a session removes them with its files.
//
// Metadata written before counters existed is initialized from the session file
// when the store opens. That initialization counts each message once, even when
// an earlier alias promotion copied it into a second file. A session that still
// lacks counters when its usage is read, such as one whose counters were reset,
// is initialized from its own file.

// opaqueKeyPrefix marks canonical session keys. pkg/session defines the same
// prefix but imports this package, so the value is repeated here.
const opaqueKeyPrefix = "sk_v1_"

// SessionUsageEntry is the usage recorded for one stored session.
type SessionUsageEntry struct {
	Key     string
	Aliases []string
	Usage   usage.Summary
}

// usageCounterResetter is implemented by stores whose usage counters can be
// marked stale. Migration uses it after it replaces a session's history with
// messages whose usage the counters have not seen.
type usageCounterResetter interface {
	resetUsageCounters(sessionKey string) error
}

// usageRecord is the part of a persisted message that usage accounting reads.
// Decoding skips the content, so message bodies are not kept.
type usageRecord struct {
	CreatedAt string               `json:"created_at"`
	ModelName string               `json:"model_name"`
	Usage     *providers.UsageInfo `json:"usage"`
}

// messageID identifies a record across session files. Records written before
// timestamps existed fall back to their model and usage, numbered by how often
// that pair already occurred in the file: equal records in one file stay
// distinct, and a copy in another file matches them one for one. occurrences
// holds the counts for the file being read.
func (r usageRecord) messageID(occurrences map[string]int) string {
	if r.CreatedAt != "" {
		return r.CreatedAt
	}
	fingerprint, _ := json.Marshal(r.Usage)
	key := r.ModelName + "|" + string(fingerprint)
	occurrences[key]++
	return fmt.Sprintf("untimed|%s|%d", key, occurrences[key])
}

// sessionFile is one session of a sessions directory.
type sessionFile struct {
	key       string
	meta      SessionMeta
	jsonlPath string
}

// ReadSessionUsage reports the usage of every session stored under dir. It only
// reads: it never repairs or rewrites files, so it can run while a gateway is
// writing to the same directory. Sessions without counters are summed from
// their files, and nothing is written back.
func ReadSessionUsage(dir string) ([]SessionUsageEntry, error) {
	files, err := readSessionFiles(dir)
	if err != nil {
		return nil, err
	}
	var legacy []sessionFile
	for _, f := range files {
		if !f.meta.UsageTracked {
			legacy = append(legacy, f)
		}
	}
	sums := legacyUsage(legacy)

	entries := make([]SessionUsageEntry, 0, len(files))
	for _, f := range files {
		var summary usage.Summary
		if f.meta.UsageTracked {
			if f.meta.Usage != nil {
				summary = *f.meta.Usage
			}
		} else {
			summary = sums[f.key]
		}
		entries = append(entries, SessionUsageEntry{
			Key:     f.key,
			Aliases: f.meta.Aliases,
			Usage:   summary,
		})
	}
	return entries, nil
}

// SessionUsage returns the cumulative usage recorded for sessionKey. A stored
// session without counters is summed from its file once and the counters are
// saved, so the next call reads the metadata only. It never rewrites messages.
func (s *JSONLStore) SessionUsage(_ context.Context, sessionKey string) (usage.Summary, error) {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return usage.Summary{}, err
	}
	if !meta.UsageTracked {
		// A session with no metadata file is not stored yet: nothing to save.
		if _, statErr := os.Stat(s.metaPath(sessionKey)); statErr == nil {
			s.ensureUsageTracked(sessionKey, &meta)
			if meta.UsageTracked {
				if err := s.writeMeta(sessionKey, meta); err != nil {
					log.Printf("memory: save usage counters of %q: %v", sessionKey, err)
				}
			}
		}
	}
	return usageOf(s.jsonlPath(sessionKey), meta), nil
}

// usageOf returns the usage recorded for a session. A session without counters
// is summed from its file. An unreadable file is logged and reported as no usage.
func usageOf(jsonlPath string, meta SessionMeta) usage.Summary {
	if meta.UsageTracked {
		if meta.Usage == nil {
			return usage.Summary{}
		}
		return *meta.Usage
	}
	sum, err := sumUsageFile(jsonlPath)
	if err != nil {
		log.Printf("memory: cannot sum usage of %s: %v", filepath.Base(jsonlPath), err)
		return usage.Summary{}
	}
	return sum
}

// sumUsageFile adds up every usage-bearing record of one session file. The
// counters describe spend, so truncated records count too.
func sumUsageFile(path string) (usage.Summary, error) {
	var sum usage.Summary
	err := eachUsageRecord(path, func(rec usageRecord) {
		sum.AddMessage(rec.ModelName, *rec.Usage)
	})
	return sum, err
}

// eachUsageRecord calls visit for every record of a session file that carries
// usage. Lines are read whole, so no record is cut by a size limit, and memory
// holds one line at a time. A malformed line, such as a partial write after a
// crash, is skipped and logged; the records around it still count.
func eachUsageRecord(path string, visit func(usageRecord)) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	malformed := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var rec usageRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				malformed++
			} else if rec.Usage != nil {
				visit(rec)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("memory: read jsonl: %w", readErr)
		}
	}
	if malformed > 0 {
		log.Printf("memory: skipped %d malformed record(s) in %s while summing usage",
			malformed, filepath.Base(path))
	}
	return nil
}

// legacyUsage sums sessions that have no counters yet, counting each message
// once. Promotion used to copy a session's history into its canonical key
// without moving the usage, so one message can sit in two files. Sessions are
// visited canonical keys first, so the live session keeps the messages it
// holds, and later files skip messages another session already counted. A
// session whose file cannot be read is left out of the result.
func legacyUsage(files []sessionFile) map[string]usage.Summary {
	sortForUsage(files)
	countedBy := make(map[string]string)
	sums := make(map[string]usage.Summary, len(files))
	for _, f := range files {
		var sum usage.Summary
		var claimed []string
		occurrences := make(map[string]int)
		err := eachUsageRecord(f.jsonlPath, func(rec usageRecord) {
			id := rec.messageID(occurrences)
			owner, seen := countedBy[id]
			if seen && owner != f.key {
				return
			}
			if !seen {
				countedBy[id] = f.key
				claimed = append(claimed, id)
			}
			sum.AddMessage(rec.ModelName, *rec.Usage)
		})
		if err != nil {
			for _, id := range claimed {
				delete(countedBy, id)
			}
			log.Printf("memory: cannot sum usage of %s: %v", filepath.Base(f.jsonlPath), err)
			continue
		}
		sums[f.key] = sum
	}
	return sums
}

// sortForUsage orders sessions with canonical keys first, then by key.
func sortForUsage(files []sessionFile) {
	sort.SliceStable(files, func(i, j int) bool {
		canonicalI := strings.HasPrefix(files[i].key, opaqueKeyPrefix)
		canonicalJ := strings.HasPrefix(files[j].key, opaqueKeyPrefix)
		if canonicalI != canonicalJ {
			return canonicalI
		}
		return files[i].key < files[j].key
	})
}

// readSessionFiles lists the sessions of a directory from their metadata files.
// A missing directory holds no sessions.
func readSessionFiles(dir string) ([]sessionFile, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: read session directory: %w", err)
	}

	var files []sessionFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		metaPath := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(metaPath)
		if readErr != nil {
			log.Printf("memory: skipping unreadable meta %s: %v", entry.Name(), readErr)
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil || meta.Key == "" {
			log.Printf("memory: skipping corrupt meta %s", entry.Name())
			continue
		}
		files = append(files, sessionFile{
			key:       meta.Key,
			meta:      meta,
			jsonlPath: strings.TrimSuffix(metaPath, ".meta.json") + ".jsonl",
		})
	}
	return files, nil
}

// backfillUsage initializes the counters of the sessions that lack them. It
// runs at startup over every session together, so each message is counted once.
func (s *JSONLStore) backfillUsage(files []sessionFile) {
	var pending []sessionFile
	for _, f := range files {
		if !f.meta.UsageTracked {
			pending = append(pending, f)
		}
	}
	if len(pending) == 0 {
		return
	}

	sums := legacyUsage(pending)
	for _, f := range pending {
		sum, ok := sums[f.key]
		if !ok {
			continue
		}
		meta := f.meta
		meta.Usage = nonEmptyUsage(sum)
		meta.UsageTracked = true
		if err := s.writeMeta(f.key, meta); err != nil {
			log.Printf("memory: save usage counters of %q: %v", f.key, err)
		}
	}
}

// ensureUsageTracked initializes the counters of one session from its file.
// Callers hold the session lock and call it before the file changes, or after a
// message was appended, so the sum matches the file. Startup initializes the
// sessions it finds; this covers the rest. It does not look at other sessions,
// so it can count a message copied by an old promotion under both keys.
func (s *JSONLStore) ensureUsageTracked(sessionKey string, meta *SessionMeta) {
	if meta.UsageTracked {
		return
	}
	sum, err := sumUsageFile(s.jsonlPath(sessionKey))
	if err != nil {
		log.Printf("memory: cannot initialize usage counters of %q: %v", sessionKey, err)
		return
	}
	meta.Usage = nonEmptyUsage(sum)
	meta.UsageTracked = true
}

// recordUsage folds a message that was just appended into the session counters.
// Callers hold the session lock and have already appended msg to the file.
func (s *JSONLStore) recordUsage(sessionKey string, meta *SessionMeta, msg providers.Message) {
	if !meta.UsageTracked {
		s.ensureUsageTracked(sessionKey, meta)
		return
	}
	if msg.Usage == nil || !msg.Usage.HasUsage() {
		return
	}
	if meta.Usage == nil {
		meta.Usage = &usage.Summary{}
	}
	meta.Usage.AddMessage(msg.ModelName, *msg.Usage)
}

// resetUsageCounters marks the counters stale so they are rebuilt from the
// session file on the next read or write.
func (s *JSONLStore) resetUsageCounters(sessionKey string) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	meta.Usage = nil
	meta.UsageTracked = false
	return s.writeMeta(sessionKey, meta)
}

// nonEmptyUsage returns the summary, or nil when it holds no usage, so sessions
// without usage do not carry an empty block in their metadata.
func nonEmptyUsage(sum usage.Summary) *usage.Summary {
	if sum.Empty() {
		return nil
	}
	return &sum
}

// mergeUsage returns the sum of two summaries with models in a stable order.
func mergeUsage(a, b usage.Summary) usage.Summary {
	a.Merge(b)
	a.SortModels()
	return a
}
