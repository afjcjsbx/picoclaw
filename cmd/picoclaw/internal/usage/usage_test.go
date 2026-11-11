package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func openSessions(t *testing.T) (string, *memory.JSONLStore) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return dir, store
}

func addUsage(t *testing.T, store *memory.JSONLStore, key, model string, prompt, completion int) {
	t.Helper()
	err := store.AddFullMessage(context.Background(), key, providers.Message{
		Role:      "assistant",
		Content:   "reply",
		ModelName: model,
		Usage: &providers.UsageInfo{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			Requests:         1,
		},
	})
	if err != nil {
		t.Fatalf("AddFullMessage(%q): %v", key, err)
	}
}

func seedOverview(t *testing.T) string {
	t.Helper()
	dir, store := openSessions(t)
	addUsage(t, store, "session-a", "model-a", 1000, 200)
	addUsage(t, store, "session-a", "model-b", 100, 50)
	addUsage(t, store, "session-b", "model-c", 100, 50)
	if err := store.AddMessage(context.Background(), "session-empty", "user", "no usage"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	return dir
}

func decodeReport(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	return decoded
}

func TestRunUsageOverviewText(t *testing.T) {
	dir := seedOverview(t)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Usage summary",
		"Sessions: 2",
		"LLM calls: 3 · turns: 3",
		"Input:  1,200 tokens",
		"Output: 300 tokens",
		"Total:  1,500 tokens",
		"Cost:   not reported",
		"session-a",
		"session-b",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("overview missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "session-a") > strings.Index(text, "session-b") {
		t.Fatalf("sessions not ordered by tokens:\n%s", text)
	}
	if strings.Contains(text, "session-empty") {
		t.Fatalf("sessions without usage must be omitted:\n%s", text)
	}
}

func TestRunUsageLimitAppliesToJSON(t *testing.T) {
	dir := seedOverview(t)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{JSON: true, Limit: 1}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	decoded := decodeReport(t, out.Bytes())
	if decoded["session_count"] != float64(2) {
		t.Fatalf("session_count = %v, want 2", decoded["session_count"])
	}
	sessions, _ := decoded["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d entries, want the limit of 1", len(sessions))
	}
	first, _ := sessions[0].(map[string]any)
	if first["key"] != "session-a" {
		t.Fatalf("first session = %v, want session-a", first["key"])
	}
	totals, _ := decoded["totals"].(map[string]any)
	if totals["turns"] != float64(3) {
		t.Fatalf("totals.turns = %v, want 3 across all sessions", totals["turns"])
	}
}

func TestRunUsageJSONShape(t *testing.T) {
	dir := seedOverview(t)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{JSON: true, Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	decoded := decodeReport(t, out.Bytes())
	for _, key := range []string{"sessions_dir", "session_count", "totals", "sessions"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("report missing %q: %v", key, decoded)
		}
	}
	sessions, _ := decoded["sessions"].([]any)
	first, _ := sessions[0].(map[string]any)
	for _, key := range []string{"key", "label", "usage"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("session row missing %q: %v", key, first)
		}
	}
	usage, _ := first["usage"].(map[string]any)
	if usage["input_tokens"] != float64(1100) || usage["models"] == nil {
		t.Fatalf("session usage = %v", usage)
	}
}

func TestRunUsageJSONEmptyListsAsArray(t *testing.T) {
	var out bytes.Buffer
	if err := runUsage(&out, filepath.Join(t.TempDir(), "absent"), options{JSON: true, Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	decoded := decodeReport(t, out.Bytes())
	if sessions, ok := decoded["sessions"].([]any); !ok || len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want an empty array", decoded["sessions"])
	}
}

func TestRunUsageMissingDirectory(t *testing.T) {
	var out bytes.Buffer
	if err := runUsage(&out, filepath.Join(t.TempDir(), "absent"), options{Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if !strings.Contains(out.String(), "No token usage recorded under") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsageSessionByExactLegacyKey(t *testing.T) {
	dir := seedOverview(t)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "session-a", Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Usage for session session-a") {
		t.Fatalf("session output missing key:\n%s", text)
	}
	if !strings.Contains(text, "By model") || !strings.Contains(text, "model-a") || !strings.Contains(text, "model-b") {
		t.Fatalf("session output missing model breakdown:\n%s", text)
	}
}

func TestRunUsageSessionByAliasOfCanonicalSession(t *testing.T) {
	dir, store := openSessions(t)
	ctx := context.Background()
	if err := store.UpsertSessionMeta(ctx, "sk_v1_chat", nil, []string{"cli:default", "agent:main:main"}); err != nil {
		t.Fatalf("UpsertSessionMeta: %v", err)
	}
	addUsage(t, store, "sk_v1_chat", "m", 100, 10)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "cli:default", Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if !strings.Contains(out.String(), "Usage for session cli:default") || !strings.Contains(out.String(), "Input:  100 tokens") {
		t.Fatalf("alias output = %q", out.String())
	}
}

func TestRunUsageSessionAliasIgnoresLegacyClaims(t *testing.T) {
	dir, store := openSessions(t)
	ctx := context.Background()
	const shared = "agent:main:direct:1"
	if err := store.UpsertSessionMeta(ctx, "agent:cron-1", nil, []string{shared}); err != nil {
		t.Fatalf("UpsertSessionMeta: %v", err)
	}
	addUsage(t, store, "agent:cron-1", "cron-model", 999, 1)
	if err := store.UpsertSessionMeta(ctx, "sk_v1_chat", nil, []string{shared}); err != nil {
		t.Fatalf("UpsertSessionMeta: %v", err)
	}
	addUsage(t, store, "sk_v1_chat", "m", 100, 10)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: shared, Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if strings.Contains(out.String(), "999") {
		t.Fatalf("legacy session claimed the alias of a canonical chat:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Input:  100 tokens") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsageSessionAmbiguousAliasAsksForKey(t *testing.T) {
	dir, store := openSessions(t)
	ctx := context.Background()
	for _, key := range []string{"sk_v1_one", "sk_v1_two"} {
		if err := store.UpsertSessionMeta(ctx, key, nil, []string{"shared-alias"}); err != nil {
			t.Fatalf("UpsertSessionMeta: %v", err)
		}
		addUsage(t, store, key, "m", 1, 1)
	}

	err := runUsage(&bytes.Buffer{}, dir, options{Session: "shared-alias", Limit: 20})
	if err == nil || !strings.Contains(err.Error(), "matches several sessions") ||
		!strings.Contains(err.Error(), "sk_v1_one, sk_v1_two") {
		t.Fatalf("err = %v, want an ambiguity error listing both keys", err)
	}
}

func TestRunUsageSessionNotFound(t *testing.T) {
	dir := seedOverview(t)

	err := runUsage(&bytes.Buffer{}, dir, options{Session: "missing", Limit: 20})
	if err == nil || !strings.Contains(err.Error(), `session "missing" not found`) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestRunUsageSessionWithoutUsage(t *testing.T) {
	dir, store := openSessions(t)
	if err := store.AddMessage(context.Background(), "plain", "user", "hello"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "plain", Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if !strings.Contains(out.String(), `No token usage recorded for session "plain".`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsageSessionJSON(t *testing.T) {
	dir := seedOverview(t)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "session-a", JSON: true, Limit: 20}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	decoded := decodeReport(t, out.Bytes())
	sessions, _ := decoded["sessions"].([]any)
	if decoded["session_count"] != float64(1) || len(sessions) != 1 {
		t.Fatalf("report = %v", decoded)
	}
	first, _ := sessions[0].(map[string]any)
	if first["key"] != "session-a" {
		t.Fatalf("key = %v, want session-a", first["key"])
	}
}
