package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type scriptedMemoryProvider struct {
	mu        sync.Mutex
	responses []string
	messages  [][]providers.Message
}

func (p *scriptedMemoryProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	copied := append([]providers.Message(nil), messages...)
	p.messages = append(p.messages, copied)

	idx := len(p.messages) - 1
	content := "Mock response"
	if idx < len(p.responses) {
		content = p.responses[idx]
	}
	return &providers.LLMResponse{Content: content}, nil
}

func (p *scriptedMemoryProvider) GetDefaultModel() string {
	return "test-model"
}

func (p *scriptedMemoryProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

func (p *scriptedMemoryProvider) callMessages(idx int) []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.messages) {
		return nil
	}
	return append([]providers.Message(nil), p.messages[idx]...)
}

func loadMemoryIndexState(t *testing.T, ms *MemoryStore) memoryIndexState {
	t.Helper()
	ms.mu.Lock()
	defer ms.mu.Unlock()

	state, err := ms.loadIndexStateLocked()
	if err != nil {
		t.Fatalf("loadIndexStateLocked() error = %v", err)
	}
	return state
}

func TestMemoryStore_PersistTurnMemoryRecord_ReordersByAccessAndPreservesManualPrefix(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)

	manualPrefix := "# Manual Memory\n\nUser prefers concise replies."
	if err := ms.WriteLongTerm(manualPrefix); err != nil {
		t.Fatalf("WriteLongTerm() error = %v", err)
	}

	if err := ms.PersistTurnMemoryRecord(generatedMemoryRecord{
		Description:     "First memory",
		SummaryMarkdown: "## First\n\nRemember the initial preference.",
	}, "session-a", nil); err != nil {
		t.Fatalf("PersistTurnMemoryRecord(first) error = %v", err)
	}

	if err := ms.PersistTurnMemoryRecord(generatedMemoryRecord{
		Description:     "Second memory",
		SummaryMarkdown: "## Second\n\nRemember the follow-up task.",
	}, "session-a", nil); err != nil {
		t.Fatalf("PersistTurnMemoryRecord(second) error = %v", err)
	}

	state := loadMemoryIndexState(t, ms)
	if len(state.Entries) != 2 {
		t.Fatalf("len(state.Entries) = %d, want 2", len(state.Entries))
	}

	var firstPath string
	for _, entry := range state.Entries {
		if entry.Description == "First memory" {
			firstPath = entry.Path
			break
		}
	}
	if firstPath == "" {
		t.Fatal("failed to locate first memory path")
	}

	if err := ms.PersistTurnMemoryRecord(generatedMemoryRecord{
		Description:     "Third memory",
		SummaryMarkdown: "## Third\n\nRemember the latest decision.",
	}, "session-a", []string{firstPath, firstPath}); err != nil {
		t.Fatalf("PersistTurnMemoryRecord(third) error = %v", err)
	}

	state = loadMemoryIndexState(t, ms)
	if len(state.Entries) != 3 {
		t.Fatalf("len(state.Entries) = %d, want 3", len(state.Entries))
	}
	if state.Entries[0].Path != firstPath {
		t.Fatalf("top memory path = %q, want %q", state.Entries[0].Path, firstPath)
	}
	if state.Entries[0].AccessCount != 2 {
		t.Fatalf("top memory access_count = %d, want 2", state.Entries[0].AccessCount)
	}

	rendered, err := os.ReadFile(filepath.Join(workspace, "memory", "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	content := string(rendered)
	if !strings.Contains(content, manualPrefix) {
		t.Fatalf("MEMORY.md missing preserved manual prefix:\n%s", content)
	}
	if !strings.Contains(content, memoryIndexStartMarker) || !strings.Contains(content, memoryIndexEndMarker) {
		t.Fatalf("MEMORY.md missing managed markers:\n%s", content)
	}
	if !strings.Contains(content, firstPath) {
		t.Fatalf("MEMORY.md missing indexed path %q:\n%s", firstPath, content)
	}
}

func TestMemoryStore_PersistTurnMemoryRecord_PrunesToMaxEntries(t *testing.T) {
	workspace := t.TempDir()
	ms := NewMemoryStore(workspace)

	known := make(map[string]struct{})
	createdPaths := make([]string, 0, memoryIndexMaxEntries+5)

	for i := 0; i < memoryIndexMaxEntries+5; i++ {
		record := generatedMemoryRecord{
			Description:     fmt.Sprintf("Memory %03d", i),
			SummaryMarkdown: fmt.Sprintf("## Memory %03d\n\nSynthetic summary.", i),
		}
		if err := ms.PersistTurnMemoryRecord(record, "session-prune", nil); err != nil {
			t.Fatalf("PersistTurnMemoryRecord(%d) error = %v", i, err)
		}

		state := loadMemoryIndexState(t, ms)
		var newPath string
		for _, entry := range state.Entries {
			if _, ok := known[entry.Path]; !ok {
				newPath = entry.Path
				break
			}
		}
		if newPath == "" {
			t.Fatalf("failed to discover newly created path after iteration %d", i)
		}
		known[newPath] = struct{}{}
		createdPaths = append(createdPaths, newPath)
	}

	state := loadMemoryIndexState(t, ms)
	if len(state.Entries) != memoryIndexMaxEntries {
		t.Fatalf("len(state.Entries) = %d, want %d", len(state.Entries), memoryIndexMaxEntries)
	}

	prunedCount := len(createdPaths) - memoryIndexMaxEntries
	for _, relPath := range createdPaths[:prunedCount] {
		absPath := filepath.Join(workspace, filepath.FromSlash(relPath))
		if _, err := os.Stat(absPath); !os.IsNotExist(err) {
			t.Fatalf("expected pruned record %q to be deleted, stat err = %v", relPath, err)
		}
	}
	for _, relPath := range createdPaths[prunedCount:] {
		absPath := filepath.Join(workspace, filepath.FromSlash(relPath))
		if _, err := os.Stat(absPath); err != nil {
			t.Fatalf("expected kept record %q to exist, stat err = %v", relPath, err)
		}
	}
}

func TestProcessMessage_PersistsGeneratedMemoryAtTurnEnd(t *testing.T) {
	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	provider := &scriptedMemoryProvider{
		responses: []string{
			"I'll remember that preference.",
			`{"description":"User wants concise replies","summary_markdown":"## Richiesta\n- Remember that I want concise replies.\n\n## Azioni\n- The assistant acknowledged the preference.\n\n## Esito\n- The preference was captured for future replies.\n\n## Fatti duraturi\n- The user wants concise replies.\n\n## Follow-up\n- Nessun follow-up esplicito."}`,
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)

	response, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "Remember that I want concise replies.",
	})
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "I'll remember that preference." {
		t.Fatalf("processMessage() response = %q, want %q", response, "I'll remember that preference.")
	}
	if provider.callCount() != 2 {
		t.Fatalf("provider callCount = %d, want 2", provider.callCount())
	}

	secondCall := provider.callMessages(1)
	if len(secondCall) < 2 {
		t.Fatalf("memory synthesis call had %d messages, want at least 2", len(secondCall))
	}
	if !strings.Contains(secondCall[0].Content, "Generate a durable memory record") {
		t.Fatalf("memory synthesis system prompt mismatch:\n%s", secondCall[0].Content)
	}

	memoryFile := filepath.Join(workspace, "memory", "MEMORY.md")
	content, err := os.ReadFile(memoryFile)
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	if !strings.Contains(string(content), "User wants concise replies") {
		t.Fatalf("MEMORY.md missing generated description:\n%s", string(content))
	}
	if !strings.Contains(string(content), "memory/records/") {
		t.Fatalf("MEMORY.md missing detailed memory path:\n%s", string(content))
	}

	records, err := filepath.Glob(filepath.Join(workspace, "memory", "records", "*", "*.md"))
	if err != nil {
		t.Fatalf("Glob(records) error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}

	recordContent, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatalf("ReadFile(record) error = %v", err)
	}
	if !strings.Contains(string(recordContent), "The user wants concise replies.") {
		t.Fatalf("record file missing synthesized summary:\n%s", string(recordContent))
	}
	if !strings.Contains(string(recordContent), "## Richiesta") || !strings.Contains(string(recordContent), "## Follow-up") {
		t.Fatalf("record file missing structured memory sections:\n%s", string(recordContent))
	}
}

func TestNormalizeGeneratedMemoryRecord_SanitizesDescription(t *testing.T) {
	record := normalizeGeneratedMemoryRecord(generatedMemoryRecord{
		Description:     "## Ecco il meteo per **Veroli** oggi:\n\n## 🌤️ Condizioni",
		SummaryMarkdown: "## Summary\n\nClean summary.",
	}, turnMemorySnapshot{})

	if record.Description != "Ecco il meteo per Veroli oggi" {
		t.Fatalf("record.Description = %q, want %q", record.Description, "Ecco il meteo per Veroli oggi")
	}
}

func TestFallbackGeneratedMemoryRecord_PrefersUserAndOmitsToolNoise(t *testing.T) {
	snapshot := turnMemorySnapshot{
		TurnHistory: []providers.Message{
			{Role: "user", Content: "Che tempo fa a Veroli oggi?"},
			{Role: "tool", Content: "{\n  \"status\": 200,\n  \"text\": \"structured output\"\n}"},
			{Role: "tool", Content: "STDERR:\nTraceback (most recent call last):\nModuleNotFoundError: No module named 'weather'"},
			{Role: "assistant", Content: "Ecco il meteo per **Veroli** oggi:\n\n## 🌤️ Condizioni attuali"},
		},
	}

	record := fallbackGeneratedMemoryRecord(snapshot)
	if record.Description != "Che tempo fa a Veroli oggi" {
		t.Fatalf("record.Description = %q, want %q", record.Description, "Che tempo fa a Veroli oggi")
	}
	if strings.Contains(record.SummaryMarkdown, "ModuleNotFoundError") {
		t.Fatalf("fallback summary should omit tool traceback noise:\n%s", record.SummaryMarkdown)
	}
	if strings.Contains(record.SummaryMarkdown, `"status": 200`) {
		t.Fatalf("fallback summary should omit raw tool JSON:\n%s", record.SummaryMarkdown)
	}
	if !strings.Contains(record.SummaryMarkdown, "## Richiesta") ||
		!strings.Contains(record.SummaryMarkdown, "## Azioni") ||
		!strings.Contains(record.SummaryMarkdown, "## Esito") ||
		!strings.Contains(record.SummaryMarkdown, "## Fatti duraturi") ||
		!strings.Contains(record.SummaryMarkdown, "## Follow-up") {
		t.Fatalf("fallback summary missing structured sections:\n%s", record.SummaryMarkdown)
	}
	if !strings.Contains(record.SummaryMarkdown, "- Che tempo fa a Veroli oggi?") {
		t.Fatalf("fallback summary missing user request in structured section:\n%s", record.SummaryMarkdown)
	}
	if !strings.Contains(record.SummaryMarkdown, "- Ecco il meteo per Veroli oggi:") {
		t.Fatalf("fallback summary missing sanitized assistant excerpt:\n%s", record.SummaryMarkdown)
	}
}

func TestBuildTurnMemorySnapshot_UsesOnlyCurrentTurnHistory(t *testing.T) {
	workspace := t.TempDir()
	agent := &AgentInstance{
		Workspace: workspace,
		Sessions:  initSessionStore(filepath.Join(workspace, "sessions")),
	}

	sessionKey := "session-turn-memory"
	agent.Sessions.SetSummary(sessionKey, "Old session summary about unrelated topics.")
	agent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "old weather question"},
		{Role: "assistant", Content: "old weather answer"},
	})

	turnMessages := []providers.Message{
		{Role: "user", Content: "new fishing question"},
		{Role: "assistant", Content: "new fishing answer"},
	}

	snapshot := buildTurnMemorySnapshot(agent, sessionKey, turnMessages)
	if snapshot.SessionSummary != "Old session summary about unrelated topics." {
		t.Fatalf("snapshot.SessionSummary = %q", snapshot.SessionSummary)
	}
	if len(snapshot.TurnHistory) != 2 {
		t.Fatalf("len(snapshot.TurnHistory) = %d, want 2", len(snapshot.TurnHistory))
	}
	if snapshot.TurnHistory[0].Content != "new fishing question" {
		t.Fatalf("unexpected turn history content: %+v", snapshot.TurnHistory)
	}
}
