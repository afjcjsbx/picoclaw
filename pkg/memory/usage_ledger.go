package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
)

// Usage ledger
//
// Session counters are running totals without timestamps, so they cannot say
// when tokens were spent. The ledger is an append-only file next to the
// sessions with one line per usage-bearing assistant message and model. It
// survives truncation, compaction, and deleting the session. Only usage
// written after the ledger existed is in it; older usage is in the counters
// alone. A failed ledger write is logged and never fails the message.
//
// ponytail: unbounded file, read whole per report. Roll old lines up into daily
// buckets if it grows past what a report can scan quickly.

// ledgerFile is not a *.jsonl or *.meta.json name, so the session scans of this
// directory never mistake it for a session.
const ledgerFile = "usage-ledger.ndjson"

// maxLedgerLine bounds one ledger line. Entries are a few hundred bytes.
const maxLedgerLine = 1024 * 1024

func ledgerPath(dir string) string { return filepath.Join(dir, ledgerFile) }

// recordLedger appends the usage of msg, once per model that served it.
func (s *JSONLStore) recordLedger(sessionKey string, msg providers.Message) {
	if msg.Usage == nil || !msg.Usage.HasUsage() || msg.CreatedAt == nil {
		return
	}
	var summary usage.Summary
	if !summary.AddMessage(msg.ModelName, *msg.Usage) {
		return
	}

	var lines []byte
	for _, m := range summary.Models {
		line, err := json.Marshal(usage.LedgerEntry{
			Time:    *msg.CreatedAt,
			Session: sessionKey,
			Model:   m.Model,
			Totals:  m.Totals,
		})
		if err != nil {
			log.Printf("memory: encode usage ledger entry: %v", err)
			return
		}
		lines = append(append(lines, line...), '\n')
	}

	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	f, err := os.OpenFile(ledgerPath(s.dir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("memory: open usage ledger: %v", err)
		return
	}
	_, writeErr := f.Write(lines)
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		log.Printf("memory: append usage ledger: %v", writeErr)
	}
}

// ReadUsageLedger returns the ledger entries of dir at or after since, and the
// time of the oldest entry of the whole ledger (zero when it is empty). It only
// reads, so it can run while a gateway appends. A missing ledger is empty and a
// malformed line, such as a partial write after a crash, is skipped.
func ReadUsageLedger(dir string, since time.Time) ([]usage.LedgerEntry, time.Time, error) {
	f, err := os.Open(ledgerPath(dir))
	if os.IsNotExist(err) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("memory: open usage ledger: %w", err)
	}
	defer f.Close()

	var (
		entries   []usage.LedgerEntry
		first     time.Time
		malformed int
	)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, maxLedgerLine)
	for scanner.Scan() {
		var e usage.LedgerEntry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil || e.Time.IsZero() {
			malformed++
			continue
		}
		if first.IsZero() || e.Time.Before(first) {
			first = e.Time
		}
		if !e.Time.Before(since) {
			entries = append(entries, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("memory: read usage ledger: %w", err)
	}
	if malformed > 0 {
		log.Printf("memory: skipped %d malformed usage ledger line(s)", malformed)
	}
	return entries, first, nil
}
