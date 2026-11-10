package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
)

func usageMessage(model string, prompt, completion int) providers.Message {
	return providers.Message{
		Role:      "assistant",
		Content:   "answer",
		ModelName: model,
		Usage: &providers.UsageInfo{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			Requests:         1,
		},
	}
}

func timedUsageMessage(at time.Time, model string, prompt, completion int) providers.Message {
	msg := usageMessage(model, prompt, completion)
	msg.CreatedAt = &at
	return msg
}

func mustSessionUsage(t *testing.T, store *JSONLStore, key string) usage.Summary {
	t.Helper()
	summary, err := store.SessionUsage(context.Background(), key)
	if err != nil {
		t.Fatalf("SessionUsage(%q): %v", key, err)
	}
	return summary
}

func totalUsage(entries []SessionUsageEntry) usage.Totals {
	var total usage.Totals
	for _, entry := range entries {
		total.Merge(entry.Usage.Totals)
	}
	return total
}

// writeLegacySession writes a session the way the store did before usage
// counters existed: a JSONL file and a metadata file without usage fields.
func writeLegacySession(t *testing.T, dir, key string, skip int, msgs ...providers.Message) {
	t.Helper()
	var body []byte
	for _, msg := range msgs {
		line, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal message: %v", err)
		}
		body = append(append(body, line...), '\n')
	}
	base := filepath.Join(dir, sanitizeKey(key))
	if err := os.WriteFile(base+".jsonl", body, 0o644); err != nil {
		t.Fatalf("WriteFile jsonl: %v", err)
	}
	meta, err := json.Marshal(SessionMeta{Key: key, Skip: skip, Count: len(msgs)})
	if err != nil {
		t.Fatalf("Marshal meta: %v", err)
	}
	if err := os.WriteFile(base+".meta.json", meta, 0o644); err != nil {
		t.Fatalf("WriteFile meta: %v", err)
	}
}

func readFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		files[entry.Name()] = string(data)
	}
	return files
}

func TestSessionUsageAccumulatesAppendedMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.AddFullMessage(ctx, "s", usageMessage("model-a", 100, 10)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	if err := store.AddFullMessage(ctx, "s", usageMessage("model-b", 200, 20)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	if err := store.AddMessage(ctx, "s", "user", "no usage here"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	got := mustSessionUsage(t, store, "s")
	if got.Turns != 2 || got.Requests != 2 || got.InputTokens != 300 || got.OutputTokens != 30 ||
		got.TotalTokens != 330 {
		t.Fatalf("usage = %+v", got.Totals)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %+v, want one row per model", got.Models)
	}
}

func TestSessionUsageSurvivesHistoryRewrites(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for range 4 {
		if err := store.AddFullMessage(ctx, "s", usageMessage("m", 100, 10)); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}
	before := mustSessionUsage(t, store, "s")

	if err := store.TruncateHistory(ctx, "s", 1); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	if history, _ := store.GetHistory(ctx, "s"); len(history) != 1 {
		t.Fatalf("visible history = %d messages, want 1", len(history))
	}
	if err := store.Compact(ctx, "s"); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if rawCount, _, _ := scanRetainedMessageLines(store.jsonlPath("s")); rawCount != 1 {
		t.Fatalf("file holds %d records after compaction, want 1", rawCount)
	}
	if got := mustSessionUsage(t, store, "s"); !reflect.DeepEqual(got, before) {
		t.Fatalf("usage after compaction = %+v, want %+v", got, before)
	}

	if err := store.SetHistory(ctx, "s", nil); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if got := mustSessionUsage(t, store, "s"); !reflect.DeepEqual(got, before) {
		t.Fatalf("usage after clearing = %+v, want %+v", got, before)
	}
}

func TestSetHistoryDoesNotCountExistingMessagesAgain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.AddFullMessage(ctx, "s", usageMessage("m", 50, 5)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	history, err := store.GetHistory(ctx, "s")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if err := store.SetHistory(ctx, "s", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	if got := mustSessionUsage(t, store, "s"); got.Turns != 1 || got.InputTokens != 50 {
		t.Fatalf("usage = %+v, want the single original turn", got.Totals)
	}
}

func TestPromoteAliasHistoryMovesUsageToCanonicalSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const alias = "agent:main:telegram:direct:42"
	const canonical = "sk_v1_canonical"
	for _, msg := range []providers.Message{usageMessage("m", 100, 10), usageMessage("m", 300, 30)} {
		if err := store.AddFullMessage(ctx, alias, msg); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}
	before := mustSessionUsage(t, store, alias)

	promoted, err := store.PromoteAliasHistory(ctx, canonical, nil, []string{alias})
	if err != nil {
		t.Fatalf("PromoteAliasHistory: %v", err)
	}
	if !promoted {
		t.Fatal("PromoteAliasHistory() = false, want promotion into an empty canonical session")
	}

	if got := mustSessionUsage(t, store, canonical); !reflect.DeepEqual(got, before) {
		t.Fatalf("canonical usage = %+v, want %+v", got, before)
	}
	if got := mustSessionUsage(t, store, alias); !got.Empty() {
		t.Fatalf("alias usage = %+v, want none after promotion", got.Totals)
	}

	entries, err := ReadSessionUsage(store.dir)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if total := totalUsage(entries); total != before.Totals {
		t.Fatalf("directory total = %+v, want %+v (promoted spend counted once)", total, before.Totals)
	}
}

func TestLegacyPromotionCopyIsCountedOnce(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	first := timedUsageMessage(at, "m", 100, 10)
	second := timedUsageMessage(at.Add(time.Minute), "m", 200, 20)
	fresh := timedUsageMessage(at.Add(2*time.Minute), "m", 300, 30)
	const alias = "agent:main:telegram:direct:42"
	const canonical = "sk_v1_chat"
	writeLegacySession(t, dir, alias, 0, first, second)
	writeLegacySession(t, dir, canonical, 0, first, second, fresh)

	before, err := ReadSessionUsage(dir)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if got := totalUsage(before); got.Turns != 3 || got.InputTokens != 600 {
		t.Fatalf("total before the store opens = %+v, want the three distinct messages", got)
	}

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	if got := mustSessionUsage(t, store, canonical); got.Turns != 3 || got.InputTokens != 600 {
		t.Fatalf("canonical usage = %+v, want all three messages", got.Totals)
	}
	if got := mustSessionUsage(t, store, alias); !got.Empty() {
		t.Fatalf("alias usage = %+v, want none: its messages now live in the canonical session", got.Totals)
	}
}

func TestLegacyAliasKeepsSpendOfTruncatedMessages(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	older := timedUsageMessage(at, "m", 100, 10)
	shared := timedUsageMessage(at.Add(time.Minute), "m", 200, 20)
	last := timedUsageMessage(at.Add(2*time.Minute), "m", 300, 30)
	fresh := timedUsageMessage(at.Add(3*time.Minute), "m", 400, 40)
	const alias = "agent:main:telegram:direct:42"
	const canonical = "sk_v1_chat"
	writeLegacySession(t, dir, alias, 1, older, shared, last)
	writeLegacySession(t, dir, canonical, 0, shared, last, fresh)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	if got := mustSessionUsage(t, store, canonical); got.Turns != 3 || got.InputTokens != 900 {
		t.Fatalf("canonical usage = %+v, want the copied and new messages", got.Totals)
	}
	if got := mustSessionUsage(t, store, alias); got.Turns != 1 || got.InputTokens != 100 {
		t.Fatalf("alias usage = %+v, want only the message that was never copied", got.Totals)
	}
}

func TestLegacySessionUsageIsSummedWhenStoreOpens(t *testing.T) {
	dir := t.TempDir()
	writeLegacySession(t, dir, "legacy:key", 1,
		usageMessage("old", 999, 1),
		usageMessage("m", 100, 10),
		usageMessage("m", 200, 20),
	)

	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	got := mustSessionUsage(t, store, "legacy:key")
	if got.Turns != 3 || got.InputTokens != 1299 {
		t.Fatalf("usage = %+v, want all records including the truncated prefix", got.Totals)
	}

	meta, err := store.GetSessionMeta(context.Background(), "legacy:key")
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if !meta.UsageTracked {
		t.Fatal("UsageTracked = false after the store opened, want counters initialized")
	}

	if err := store.AddFullMessage(context.Background(), "legacy:key", usageMessage("m", 1, 1)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	if got := mustSessionUsage(t, store, "legacy:key"); got.Turns != 4 {
		t.Fatalf("turns after a new message = %d, want 4", got.Turns)
	}
}

func TestLegacySessionKeepsSpendOfHistoryDroppedBeforeBackfill(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	writeLegacySession(t, dir, "legacy:key", 0,
		usageMessage("m", 100, 10),
		usageMessage("m", 200, 20),
	)

	if err := store.SetHistory(context.Background(), "legacy:key", nil); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if got := mustSessionUsage(t, store, "legacy:key"); got.Turns != 2 || got.InputTokens != 300 {
		t.Fatalf("usage after clearing = %+v, want the spend of the dropped messages", got.Totals)
	}
}

func TestReadSessionUsageSumsLegacySessionsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	writeLegacySession(t, dir, "legacy:key", 0,
		usageMessage("m", 100, 10),
		usageMessage("m", 200, 20),
	)
	before := readFiles(t, dir)

	entries, err := ReadSessionUsage(dir)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "legacy:key" || entries[0].Usage.Turns != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if after := readFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("ReadSessionUsage modified the session directory")
	}
}

func TestReadSessionUsageMissingDirectory(t *testing.T) {
	entries, err := ReadSessionUsage(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("ReadSessionUsage() = %+v, %v; want no entries and no error", entries, err)
	}
}

func TestSumUsageFileSkipsMalformedRecords(t *testing.T) {
	good := func(prompt int) string {
		line, err := json.Marshal(usageMessage("m", prompt, 1))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return string(line) + "\n"
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := good(100) + "not json\n" + good(200) + `{"role":"assistant","usage":{"prompt_tokens":5`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sum, err := sumUsageFile(path)
	if err != nil {
		t.Fatalf("sumUsageFile: %v", err)
	}
	if sum.Turns != 2 || sum.InputTokens != 300 {
		t.Fatalf("sum = %+v, want the two valid records", sum.Totals)
	}
}

func TestSumUsageFileReadsRecordsLargerThanTheOldLineLimit(t *testing.T) {
	big := providers.Message{
		Role:      "assistant",
		Content:   strings.Repeat("x", maxLineSize+4096),
		ModelName: "m",
		Usage:     &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6, Requests: 1},
	}
	bigLine, err := json.Marshal(big)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	small, err := json.Marshal(usageMessage("m", 7, 1))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	body := append(append(bigLine, '\n'), append(small, '\n')...)
	if err = os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sum, err := sumUsageFile(path)
	if err != nil {
		t.Fatalf("sumUsageFile: %v", err)
	}
	if sum.Turns != 2 || sum.InputTokens != 12 {
		t.Fatalf("sum = %+v, want the oversized record and the one after it", sum.Totals)
	}
}

func TestDeleteSessionRemovesUsageEntry(t *testing.T) {
	store := newTestStore(t)
	if err := store.AddFullMessage(context.Background(), "gone", usageMessage("m", 1, 1)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	if err := store.DeleteSession(context.Background(), "gone"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	entries, err := ReadSessionUsage(store.dir)
	if err != nil {
		t.Fatalf("ReadSessionUsage: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none after deletion", entries)
	}
}

func TestResetUsageCountersRebuildsFromFile(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	migrated := []providers.Message{usageMessage("m", 10, 1), usageMessage("m", 20, 2)}
	if err := store.SetHistory(ctx, "migrated", migrated); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if got := mustSessionUsage(t, store, "migrated"); got.Turns != 0 {
		t.Fatalf("SetHistory counted %d turns, want none", got.Turns)
	}

	if err := store.resetUsageCounters("migrated"); err != nil {
		t.Fatalf("resetUsageCounters: %v", err)
	}
	if got := mustSessionUsage(t, store, "migrated"); got.Turns != 2 || got.InputTokens != 30 {
		t.Fatalf("usage after reset = %+v, want the migrated messages", got.Totals)
	}
}
