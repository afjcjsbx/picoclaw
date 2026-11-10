package usage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func writeSession(t *testing.T, dir, key string, skip int, aliases []string, records ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	base := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(key)
	meta, err := json.Marshal(sessionMeta{Key: key, Skip: skip, Aliases: aliases})
	if err != nil {
		t.Fatalf("Marshal meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, base+".meta.json"), meta, 0o600); err != nil {
		t.Fatalf("WriteFile meta: %v", err)
	}
	body := strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, base+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile jsonl: %v", err)
	}
}

func usageLine(model string, usage providers.UsageInfo) string {
	line, err := json.Marshal(struct {
		Role      string              `json:"role"`
		Content   string              `json:"content"`
		ModelName string              `json:"model_name"`
		Usage     providers.UsageInfo `json:"usage"`
	}{
		Role:      "assistant",
		Content:   strings.Repeat("x", 1024),
		ModelName: model,
		Usage:     usage,
	})
	if err != nil {
		panic(err)
	}
	return string(line)
}

func seedSessions(t *testing.T, dir string) {
	t.Helper()
	writeSession(t, dir, "session-a", 0, []string{"alias-a"},
		usageLine("model-a", providers.UsageInfo{
			PromptTokens:     1000,
			CompletionTokens: 200,
			TotalTokens:      1200,
			CacheReadTokens:  800,
			CostUSD:          0.01,
			Requests:         2,
		}),
		usageLine("model-b", providers.UsageInfo{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			CostUSD:          0.002,
			Requests:         1,
		}),
	)
	writeSession(t, dir, "session-b", 0, nil,
		usageLine("model-c", providers.UsageInfo{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			CostUSD:          0.002,
			Requests:         1,
		}),
	)
	writeSession(t, dir, "session-empty", 0, nil, `{"role":"assistant","content":"no usage"}`)
}

func TestRunUsageOverview(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}

	text := out.String()
	for _, want := range []string{
		"Usage summary",
		"Sessions: 2",
		"LLM calls: 4 · turns: 3",
		"Input:  1,200 tokens (fresh 400 · cache read 800)",
		"Output: 300 tokens",
		"Total:  1,500 tokens",
		"Cost:   $0.0140",
		"session-a",
		"session-b",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("usage output missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "session-a") > strings.Index(text, "session-b") {
		t.Fatalf("sessions not ordered by tokens:\n%s", text)
	}
	if strings.Contains(text, "session-empty") {
		t.Fatalf("sessions without usage must be omitted:\n%s", text)
	}
}

func TestRunUsageSessionDetailByAlias(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "alias-a"}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "Usage for session session-a") {
		t.Fatalf("detail output missing canonical key:\n%s", text)
	}
	if !strings.Contains(text, "By model") || !strings.Contains(text, "model-a") || !strings.Contains(text, "model-b") {
		t.Fatalf("detail output missing model breakdown:\n%s", text)
	}
}

func TestRunUsageJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{JSON: true}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}

	var decoded report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SessionsDir != dir {
		t.Fatalf("SessionsDir = %q, want %q", decoded.SessionsDir, dir)
	}
	if decoded.Totals.Requests != 4 || decoded.Totals.TotalTokens != 1500 {
		t.Fatalf("totals = %+v", decoded.Totals)
	}
	if len(decoded.Sessions) != 2 || decoded.Sessions[0].Key != "session-a" {
		t.Fatalf("sessions = %+v", decoded.Sessions)
	}
	if decoded.Sessions[0].Usage.TotalTokens != 1350 {
		t.Fatalf("session-a totals = %+v", decoded.Sessions[0].Usage.Totals)
	}
}

func TestRunUsageJSONSessionIncludesModels(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{JSON: true, Session: "alias-a"}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}

	var decoded report
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Sessions) != 1 {
		t.Fatalf("sessions = %+v", decoded.Sessions)
	}
	if len(decoded.Sessions[0].Usage.Models) != 2 {
		t.Fatalf("models = %+v, want two models", decoded.Sessions[0].Usage.Models)
	}
}

func TestRunUsageHonorsSkipAndPartialTail(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, dir, "skipped", 2, nil,
		usageLine("old", providers.UsageInfo{PromptTokens: 1, CompletionTokens: 1}),
		usageLine("old", providers.UsageInfo{PromptTokens: 2, CompletionTokens: 2}),
		usageLine("kept", providers.UsageInfo{PromptTokens: 30, CompletionTokens: 5, Requests: 1}),
		`{"usage":{`,
	)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "skipped"}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Input:  30 tokens") || !strings.Contains(text, "Output: 5 tokens") {
		t.Fatalf("skip/partial-tail output = %q", text)
	}
}

func TestRunUsageSessionWithoutUsage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	writeSession(t, dir, "empty", 0, nil, `{"role":"assistant","content":"hello"}`)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Session: "empty"}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if !strings.Contains(out.String(), `No token usage recorded for session "empty"`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestRunUsageUnknownSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	err := runUsage(&bytes.Buffer{}, dir, options{Session: "missing"})
	if err == nil || !strings.Contains(err.Error(), `session "missing" not found`) {
		t.Fatalf("err = %v, want not found", err)
	}
}

func TestRunUsageNoSessions(t *testing.T) {
	var out bytes.Buffer
	if err := runUsage(&out, t.TempDir(), options{}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}
	if !strings.Contains(out.String(), "No token usage recorded under") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestSessionLabel(t *testing.T) {
	aliases := []string{
		"agent:main:main",
		"agent:main:direct:direct",
		"agent:main:cli:default:direct:direct",
		"cli:default",
	}
	if got := sessionLabel("sk_v1_abc", aliases); got != "cli:default" {
		t.Fatalf("sessionLabel(opaque) = %q, want cli:default", got)
	}
	if got := sessionLabel("agent:main:cli:direct:direct", aliases); got != "agent:main:cli:direct:direct" {
		t.Fatalf("sessionLabel(canonical) = %q", got)
	}
	if got := sessionLabel("sk_v1_abc", []string{"agent:main:main"}); got != "sk_v1_abc" {
		t.Fatalf("sessionLabel(no usable alias) = %q", got)
	}
}

func TestRunUsageLimit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	seedSessions(t, dir)

	var out bytes.Buffer
	if err := runUsage(&out, dir, options{Limit: 1}); err != nil {
		t.Fatalf("runUsage: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "top 1 of 2") {
		t.Fatalf("output missing limit label:\n%s", text)
	}
	if strings.Contains(text, "session-b") {
		t.Fatalf("output should hide the second session:\n%s", text)
	}
	if !strings.Contains(text, "Use --limit 0 to list all 2 sessions.") {
		t.Fatalf("output missing hint:\n%s", text)
	}
}
