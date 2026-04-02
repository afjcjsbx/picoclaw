package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	memoryIndexStartMarker    = "<!-- PICOCLOW_MEMORY_INDEX_START -->"
	memoryIndexEndMarker      = "<!-- PICOCLOW_MEMORY_INDEX_END -->"
	memoryIndexMaxEntries     = 200
	memoryDescriptionMaxWords = 10
	memoryPromptMaxChars      = 48 * 1024
	memoryMessageMaxChars     = 2 * 1024
	memorySummaryMaxTokens    = 1200
	memorySummaryTemperature  = 0.2
)

var memorySummarySections = []string{
	"Richiesta",
	"Azioni",
	"Esito",
	"Fatti duraturi",
	"Follow-up",
}

type generatedMemoryRecord struct {
	Description     string `json:"description"`
	SummaryMarkdown string `json:"summary_markdown"`
}

type memoryIndexEntry struct {
	ID             string    `json:"id"`
	Path           string    `json:"path"`
	Description    string    `json:"description"`
	AccessCount    int       `json:"access_count"`
	SessionKey     string    `json:"session_key,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	LastAccessedAt time.Time `json:"last_accessed_at,omitempty"`
}

type memoryIndexState struct {
	Entries []memoryIndexEntry `json:"entries"`
}

type turnMemorySnapshot struct {
	SessionKey     string
	SessionSummary string
	TurnHistory    []providers.Message
}

func (al *AgentLoop) persistTurnMemoryForTurn(ts *turnState) {
	if ts == nil || ts.agent == nil || ts.opts.NoHistory || ts.sessionKey == "" {
		return
	}
	if ts.agent.ContextBuilder == nil || ts.agent.ContextBuilder.memory == nil {
		return
	}

	memCtx, cancel := context.WithTimeout(context.Background(), 450*time.Second)
	defer cancel()

	persistedMessages := ts.persistedMessagesSnapshot()
	snapshot := buildTurnMemorySnapshot(ts.agent, ts.sessionKey, persistedMessages)
	accessedPaths := ts.agent.ContextBuilder.memory.extractAccessedRecordPaths(persistedMessages)

	record, err := al.generateTurnMemoryRecord(memCtx, ts.agent, snapshot)
	if err != nil {
		logger.WarnCF("memory", "LLM memory synthesis failed; using fallback record", map[string]any{
			"session_key": ts.sessionKey,
			"error":       err.Error(),
		})
		record = fallbackGeneratedMemoryRecord(snapshot)
	}

	if err := ts.agent.ContextBuilder.memory.PersistTurnMemoryRecord(record, ts.sessionKey, accessedPaths); err != nil {
		logger.WarnCF("memory", "Failed to persist turn memory", map[string]any{
			"session_key": ts.sessionKey,
			"error":       err.Error(),
		})
		return
	}

	ts.agent.ContextBuilder.InvalidateCache()
}

func buildTurnMemorySnapshot(
	agent *AgentInstance,
	sessionKey string,
	turnMessages []providers.Message,
) turnMemorySnapshot {
	if agent == nil || agent.Sessions == nil {
		return turnMemorySnapshot{SessionKey: sessionKey}
	}
	return turnMemorySnapshot{
		SessionKey:     sessionKey,
		SessionSummary: strings.TrimSpace(agent.Sessions.GetSummary(sessionKey)),
		TurnHistory:    append([]providers.Message(nil), turnMessages...),
	}
}

func (al *AgentLoop) generateTurnMemoryRecord(
	ctx context.Context,
	agent *AgentInstance,
	snapshot turnMemorySnapshot,
) (generatedMemoryRecord, error) {
	provider, model := selectMemoryWriterProvider(agent)
	if provider == nil {
		return generatedMemoryRecord{}, fmt.Errorf("memory synthesis provider is nil")
	}

	prompt := buildTurnMemoryPrompt(snapshot)
	if strings.TrimSpace(prompt) == "" {
		return fallbackGeneratedMemoryRecord(snapshot), nil
	}

	options := map[string]any{
		"max_tokens":       min(agent.MaxTokens, memorySummaryMaxTokens),
		"temperature":      memorySummaryTemperature,
		"prompt_cache_key": agent.ID + ":memory-turn",
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		al.activeRequests.Add(1)
		resp, err := provider.Chat(ctx, []providers.Message{
			{
				Role: "system",
				Content: "Generate a durable memory record for an AI assistant. " +
					"Return strict JSON only with keys " +
					"`description` and `summary_markdown`. " +
					"`description` must be concrete and at most 10 words. " +
					"`summary_markdown` must be detailed, factual Markdown using exactly these section headings in order: " +
					"`## Richiesta`, `## Azioni`, `## Esito`, `## Fatti duraturi`, `## Follow-up`. " +
					"Summarize ONLY the current turn. Use prior session background only to resolve ambiguous references. " +
					"Do not repeat unrelated earlier topics. Focus on the user's request in this turn, the actions taken, the result, durable facts, user preferences, decisions, artifacts, and unresolved follow-ups from this turn.",
			},
			{Role: "user", Content: prompt},
		}, nil, model, options)
		al.activeRequests.Done()
		if err != nil {
			lastErr = err
			continue
		}

		content := strings.TrimSpace(resp.Content)
		if content == "" {
			content = strings.TrimSpace(resp.ReasoningContent)
		}
		if content == "" {
			lastErr = fmt.Errorf("memory synthesis returned empty content")
			continue
		}

		record, parseErr := parseGeneratedMemoryRecord(content)
		if parseErr != nil {
			lastErr = parseErr
			continue
		}
		return normalizeGeneratedMemoryRecord(record, snapshot), nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("memory synthesis failed")
	}
	return generatedMemoryRecord{}, lastErr
}

func selectMemoryWriterProvider(agent *AgentInstance) (providers.LLMProvider, string) {
	if agent == nil {
		return nil, ""
	}
	if agent.LightProvider != nil && agent.Router != nil {
		return agent.LightProvider, resolvedCandidateModel(agent.LightCandidates, agent.Router.LightModel())
	}
	return agent.Provider, agent.Model
}

func buildTurnMemoryPrompt(snapshot turnMemorySnapshot) string {
	var sb strings.Builder

	sb.WriteString("Create one durable memory record from the current turn only.\n")
	sb.WriteString("Use older session background only if it is needed to resolve references in this turn.\n")
	sb.WriteString("Do not repeat unrelated earlier topics.\n\n")
	sb.WriteString("Return JSON only:\n")
	sb.WriteString("{\n")
	sb.WriteString(`  "description": "max 10 words",` + "\n")
	sb.WriteString(`  "summary_markdown": "markdown with sections Richiesta, Azioni, Esito, Fatti duraturi, Follow-up"` + "\n")
	sb.WriteString("}\n\n")

	sb.WriteString("`summary_markdown` must use exactly this structure:\n")
	sb.WriteString("## Richiesta\n- ...\n\n")
	sb.WriteString("## Azioni\n- ...\n\n")
	sb.WriteString("## Esito\n- ...\n\n")
	sb.WriteString("## Fatti duraturi\n- ...\n\n")
	sb.WriteString("## Follow-up\n- ...\n\n")

	if snapshot.SessionSummary != "" {
		sb.WriteString("Background session summary (context only, do not restate unless needed):\n")
		sb.WriteString(snapshot.SessionSummary)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Current turn transcript:\n")

	remaining := memoryPromptMaxChars - sb.Len()
	omitted := 0
	for _, msg := range snapshot.TurnHistory {
		formatted := formatMemoryPromptMessage(msg)
		if formatted == "" {
			continue
		}
		if len(formatted) > memoryMessageMaxChars {
			formatted = formatted[:memoryMessageMaxChars] + "\n[truncated]\n"
		}
		if len(formatted) > remaining {
			omitted++
			continue
		}
		sb.WriteString(formatted)
		remaining -= len(formatted)
	}

	if omitted > 0 {
		fmt.Fprintf(&sb, "\n[%d additional messages omitted due to prompt size limits]\n", omitted)
	}

	return sb.String()
}

func formatMemoryPromptMessage(msg providers.Message) string {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s\n", msg.Role, content)
}

func parseGeneratedMemoryRecord(raw string) (generatedMemoryRecord, error) {
	candidate := extractJSONObject(raw)
	var record generatedMemoryRecord
	if err := json.Unmarshal([]byte(candidate), &record); err != nil {
		return generatedMemoryRecord{}, fmt.Errorf("invalid memory synthesis json: %w", err)
	}
	return record, nil
}

func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return raw[start : end+1]
	}
	return raw
}

func normalizeGeneratedMemoryRecord(
	record generatedMemoryRecord,
	snapshot turnMemorySnapshot,
) generatedMemoryRecord {
	record.Description = sanitizeMemoryDescription(record.Description)
	record.SummaryMarkdown = normalizeStructuredMemorySummary(record.SummaryMarkdown, snapshot)
	if record.Description == "" {
		record.Description = deriveFallbackDescription(snapshot)
	}
	return record
}

func fallbackGeneratedMemoryRecord(snapshot turnMemorySnapshot) generatedMemoryRecord {
	return generatedMemoryRecord{
		Description:     deriveFallbackDescription(snapshot),
		SummaryMarkdown: normalizeStructuredMemorySummary("", snapshot),
	}
}

func deriveFallbackDescription(snapshot turnMemorySnapshot) string {
	for i := len(snapshot.TurnHistory) - 1; i >= 0; i-- {
		if snapshot.TurnHistory[i].Role != "user" {
			continue
		}
		content := sanitizeMemoryDescription(snapshot.TurnHistory[i].Content)
		if content != "" {
			return content
		}
	}
	for i := len(snapshot.TurnHistory) - 1; i >= 0; i-- {
		if snapshot.TurnHistory[i].Role != "assistant" {
			continue
		}
		content := sanitizeMemoryDescription(snapshot.TurnHistory[i].Content)
		if content != "" {
			return content
		}
	}
	if content := sanitizeMemoryDescription(snapshot.SessionSummary); content != "" {
		return content
	}
	return "Conversation memory"
}

func sanitizeMemoryDescription(text string) string {
	text = firstMeaningfulLine(text)
	text = stripMarkdownNoise(text)
	text = strings.TrimSpace(text)
	text = strings.TrimRight(text, " .,!?:;-")
	return limitWords(text, memoryDescriptionMaxWords)
}

func fallbackConversationExcerpt(msg providers.Message) string {
	switch msg.Role {
	case "user", "assistant":
		return sanitizeMemoryExcerpt(msg.Content)
	case "tool":
		return sanitizeToolExcerpt(msg.Content)
	default:
		return ""
	}
}

func sanitizeMemoryExcerpt(text string) string {
	text = firstMeaningfulLine(text)
	text = stripMarkdownNoise(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return truncateRunes(text, 240)
}

func sanitizeToolExcerpt(text string) string {
	line := firstMeaningfulLine(text)
	if line == "" {
		return ""
	}
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(trimmed, "{"),
		strings.HasPrefix(trimmed, "["),
		strings.HasPrefix(lower, "stderr:"),
		strings.HasPrefix(lower, "traceback"),
		strings.HasPrefix(lower, "invalid arguments"),
		strings.HasPrefix(lower, "[command exited"):
		return ""
	}
	return sanitizeMemoryExcerpt(trimmed)
}

func normalizeStructuredMemorySummary(raw string, snapshot turnMemorySnapshot) string {
	parsed := parseMemorySummarySections(raw)
	heuristics := buildHeuristicMemorySections(snapshot)

	if len(parsed) == 0 {
		if text := sanitizeStructuredSectionBody(raw); text != "" {
			heuristics["Esito"] = ensureBulletList(text)
		}
		return renderMemorySummarySections(heuristics)
	}

	sections := make(map[string]string, len(memorySummarySections))
	for _, section := range memorySummarySections {
		content := sanitizeStructuredSectionBody(parsed[section])
		if content == "" {
			content = heuristics[section]
		}
		sections[section] = content
	}
	return renderMemorySummarySections(sections)
}

func parseMemorySummarySections(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	sections := make(map[string]string)
	var current string
	var currentLines []string

	flush := func() {
		if current == "" {
			return
		}
		sections[current] = strings.TrimSpace(strings.Join(currentLines, "\n"))
	}

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		matched := ""
		for _, section := range memorySummarySections {
			if trimmed == "## "+section {
				matched = section
				break
			}
		}
		if matched != "" {
			flush()
			current = matched
			currentLines = currentLines[:0]
			continue
		}
		if current != "" {
			currentLines = append(currentLines, line)
		}
	}
	flush()

	if len(sections) == 0 {
		return nil
	}
	return sections
}

func buildHeuristicMemorySections(snapshot turnMemorySnapshot) map[string]string {
	return map[string]string{
		"Richiesta":      buildRequestSection(snapshot),
		"Azioni":         buildActionsSection(snapshot),
		"Esito":          buildOutcomeSection(snapshot),
		"Fatti duraturi": buildDurableFactsSection(snapshot),
		"Follow-up":      buildFollowUpSection(snapshot),
	}
}

func buildRequestSection(snapshot turnMemorySnapshot) string {
	items := collectRoleExcerpts(snapshot.TurnHistory, "user", 3)
	if len(items) == 0 {
		return "- Nessuna richiesta esplicita rilevata."
	}
	return bullets(items)
}

func buildActionsSection(snapshot turnMemorySnapshot) string {
	var items []string
	toolItems := collectRoleExcerpts(snapshot.TurnHistory, "tool", 3)
	for _, item := range toolItems {
		items = append(items, "Uso di tool o output tecnico: "+item)
	}

	assistantItems := collectRoleExcerpts(snapshot.TurnHistory, "assistant", 2)
	if len(assistantItems) > 1 {
		for _, item := range assistantItems[:len(assistantItems)-1] {
			items = append(items, "Passaggio dell'assistente: "+item)
		}
	}

	if len(items) == 0 {
		items = append(items, "Risposta generata senza passaggi intermedi rilevanti.")
	}
	return bullets(items)
}

func buildOutcomeSection(snapshot turnMemorySnapshot) string {
	assistantItems := collectRoleExcerpts(snapshot.TurnHistory, "assistant", 1)
	if len(assistantItems) > 0 {
		return bullets([]string{assistantItems[0]})
	}
	userItems := collectRoleExcerpts(snapshot.TurnHistory, "user", 1)
	if len(userItems) > 0 {
		return bullets([]string{"Turno registrato sulla richiesta: " + userItems[0]})
	}
	return "- Esito non disponibile."
}

func buildDurableFactsSection(snapshot turnMemorySnapshot) string {
	userItems := collectRoleExcerpts(snapshot.TurnHistory, "user", 3)
	var facts []string
	for _, item := range userItems {
		lower := strings.ToLower(item)
		switch {
		case strings.Contains(lower, "ricorda"):
			facts = append(facts, item)
		case strings.Contains(lower, "prefer"):
			facts = append(facts, item)
		case strings.Contains(lower, "voglio"):
			facts = append(facts, item)
		}
	}
	if len(facts) == 0 {
		return "- Nessun nuovo fatto duraturo emerso in questo turno."
	}
	return bullets(facts)
}

func buildFollowUpSection(snapshot turnMemorySnapshot) string {
	assistantItems := collectRoleExcerpts(snapshot.TurnHistory, "assistant", 1)
	if len(assistantItems) > 0 && strings.Contains(assistantItems[0], "?") {
		return bullets([]string{assistantItems[0]})
	}
	return "- Nessun follow-up esplicito."
}

func collectRoleExcerpts(history []providers.Message, role string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	items := make([]string, 0, limit)
	for i := len(history) - 1; i >= 0 && len(items) < limit; i-- {
		if history[i].Role != role {
			continue
		}
		excerpt := fallbackConversationExcerpt(history[i])
		if excerpt == "" {
			continue
		}
		items = append(items, excerpt)
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func bullets(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			sb.WriteString("- ")
			sb.WriteString(item)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func sanitizeStructuredSectionBody(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(cleaned) > 0 && cleaned[len(cleaned)-1] != "" {
				cleaned = append(cleaned, "")
			}
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func ensureBulletList(text string) string {
	text = sanitizeStructuredSectionBody(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "* ") {
		return text
	}
	return bullets([]string{text})
}

func renderMemorySummarySections(sections map[string]string) string {
	var sb strings.Builder
	for i, section := range memorySummarySections {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## ")
		sb.WriteString(section)
		sb.WriteString("\n")
		content := sanitizeStructuredSectionBody(sections[section])
		if content == "" {
			content = "- Nessun elemento rilevato."
		}
		sb.WriteString(content)
	}
	return strings.TrimSpace(sb.String())
}

func firstMeaningfulLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		return line
	}
	return ""
}

func stripMarkdownNoise(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimLeft(text, "#>*- \t")
	replacer := strings.NewReplacer(
		"**", "",
		"__", "",
		"`", "",
		"[truncated]", "",
	)
	text = replacer.Replace(text)
	return strings.Join(strings.Fields(text), " ")
}

func limitWords(text string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	return strings.Join(words, " ")
}

func truncateRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

func (ms *MemoryStore) PersistTurnMemoryRecord(
	record generatedMemoryRecord,
	sessionKey string,
	accessedPaths []string,
) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := time.Now().UTC()
	state, err := ms.loadIndexStateLocked()
	if err != nil {
		return err
	}

	state.Entries = ms.filterExistingEntriesLocked(state.Entries)
	ms.applyAccessCountsLocked(state.Entries, accessedPaths, now)

	entry, err := ms.writeMemoryRecordLocked(record, sessionKey, now)
	if err != nil {
		return err
	}
	state.Entries = append(state.Entries, entry)

	sortMemoryEntries(state.Entries)
	kept, pruned := pruneMemoryEntries(state.Entries, memoryIndexMaxEntries)
	state.Entries = kept

	if err := ms.writeIndexStateLocked(state); err != nil {
		return err
	}
	if err := ms.writeRenderedMemoryLocked(state); err != nil {
		return err
	}
	for _, entry := range pruned {
		_ = os.Remove(filepath.Join(ms.workspace, filepath.FromSlash(entry.Path)))
	}

	return nil
}

func (ms *MemoryStore) extractAccessedRecordPaths(messages []providers.Message) []string {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	var paths []string
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.Name == "" || tc.Function == nil {
				continue
			}
			switch tc.Name {
			case "read_file", "edit_file", "append_file", "write_file", "send_file":
			default:
				continue
			}

			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				continue
			}
			path, _ := args["path"].(string)
			if normalized := ms.normalizeRecordPathLocked(path); normalized != "" {
				paths = append(paths, normalized)
			}
		}
	}
	return paths
}

func (ms *MemoryStore) normalizeRecordPathLocked(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath = filepath.Clean(filepath.Join(ms.workspace, path))
	}

	rel, err := filepath.Rel(ms.workspace, absPath)
	if err != nil {
		return ""
	}
	rel = filepath.Clean(rel)
	if strings.HasPrefix(rel, "..") {
		return ""
	}

	relSlash := filepath.ToSlash(rel)
	if !strings.HasPrefix(relSlash, "memory/records/") {
		return ""
	}
	if filepath.Ext(relSlash) != ".md" {
		return ""
	}
	return relSlash
}

func (ms *MemoryStore) loadIndexStateLocked() (memoryIndexState, error) {
	data, err := os.ReadFile(ms.indexStateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return memoryIndexState{}, nil
		}
		return memoryIndexState{}, err
	}

	var state memoryIndexState
	if err := json.Unmarshal(data, &state); err != nil {
		return memoryIndexState{}, err
	}
	return state, nil
}

func (ms *MemoryStore) writeIndexStateLocked(state memoryIndexState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(ms.indexStateFile, data, 0o600)
}

func (ms *MemoryStore) filterExistingEntriesLocked(entries []memoryIndexEntry) []memoryIndexEntry {
	filtered := make([]memoryIndexEntry, 0, len(entries))
	for _, entry := range entries {
		normalized := ms.normalizeRecordPathLocked(entry.Path)
		if normalized == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(ms.workspace, filepath.FromSlash(normalized))); err != nil {
			continue
		}
		entry.Path = normalized
		entry.Description = limitWords(strings.TrimSpace(entry.Description), memoryDescriptionMaxWords)
		filtered = append(filtered, entry)
	}
	return filtered
}

func (ms *MemoryStore) applyAccessCountsLocked(entries []memoryIndexEntry, accessedPaths []string, now time.Time) {
	if len(entries) == 0 || len(accessedPaths) == 0 {
		return
	}

	counts := make(map[string]int)
	for _, path := range accessedPaths {
		if normalized := ms.normalizeRecordPathLocked(path); normalized != "" {
			counts[normalized]++
		}
	}

	for i := range entries {
		if inc := counts[entries[i].Path]; inc > 0 {
			entries[i].AccessCount += inc
			entries[i].LastAccessedAt = now
		}
	}
}

func (ms *MemoryStore) writeMemoryRecordLocked(
	record generatedMemoryRecord,
	sessionKey string,
	now time.Time,
) (memoryIndexEntry, error) {
	record = normalizeGeneratedMemoryRecord(record, turnMemorySnapshot{})

	monthDir := filepath.Join(ms.recordsDir, now.Format("200601"))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return memoryIndexEntry{}, err
	}

	id := fmt.Sprintf("%s-%d", now.Format("20060102T150405Z"), now.UnixNano())
	filename := id + ".md"
	absPath := filepath.Join(monthDir, filename)
	relPath := filepath.ToSlash(filepath.Join("memory", "records", now.Format("200601"), filename))

	content := renderMemoryRecordMarkdown(record, sessionKey, now)
	if err := fileutil.WriteFileAtomic(absPath, []byte(content), 0o600); err != nil {
		return memoryIndexEntry{}, err
	}

	return memoryIndexEntry{
		ID:          id,
		Path:        relPath,
		Description: limitWords(record.Description, memoryDescriptionMaxWords),
		SessionKey:  sessionKey,
		CreatedAt:   now,
	}, nil
}

func renderMemoryRecordMarkdown(record generatedMemoryRecord, sessionKey string, now time.Time) string {
	var sb strings.Builder
	title := strings.TrimSpace(record.Description)
	if title == "" {
		title = "Conversation memory"
	}

	sb.WriteString("# ")
	sb.WriteString(title)
	sb.WriteString("\n\n")
	fmt.Fprintf(&sb, "- Created: %s\n", now.Format(time.RFC3339))
	if sessionKey != "" {
		fmt.Fprintf(&sb, "- Session: `%s`\n", sessionKey)
	}
	sb.WriteString("\n")
	sb.WriteString(record.SummaryMarkdown)
	sb.WriteString("\n")
	return sb.String()
}

func sortMemoryEntries(entries []memoryIndexEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].AccessCount != entries[j].AccessCount {
			return entries[i].AccessCount > entries[j].AccessCount
		}
		if !entries[i].LastAccessedAt.Equal(entries[j].LastAccessedAt) {
			return entries[i].LastAccessedAt.After(entries[j].LastAccessedAt)
		}
		if !entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		}
		return entries[i].Path < entries[j].Path
	})
}

func pruneMemoryEntries(entries []memoryIndexEntry, limit int) ([]memoryIndexEntry, []memoryIndexEntry) {
	if len(entries) <= limit {
		return entries, nil
	}
	kept := append([]memoryIndexEntry(nil), entries[:limit]...)
	pruned := append([]memoryIndexEntry(nil), entries[limit:]...)
	return kept, pruned
}

func (ms *MemoryStore) writeRenderedMemoryLocked(state memoryIndexState) error {
	prefix := ms.readManualMemoryPrefixLocked()

	var sb strings.Builder
	if prefix != "" {
		sb.WriteString(strings.TrimRight(prefix, "\n"))
		sb.WriteString("\n\n")
	}

	sb.WriteString(memoryIndexStartMarker)
	sb.WriteString("\n# Memory Index\n\n")
	sb.WriteString("This section is auto-generated. ")
	sb.WriteString("Rank `1` means the most frequently accessed memory record. ")
	sb.WriteString("Use `read_file` with the listed path to open a detailed memory when relevant.\n\n")

	if len(state.Entries) == 0 {
		sb.WriteString("_No generated memories yet._\n")
	} else {
		for i, entry := range state.Entries {
			fmt.Fprintf(
				&sb,
				"%d. rank=%d | accesses=%d | %s | path=`%s`\n",
				i+1,
				i+1,
				entry.AccessCount,
				entry.Description,
				entry.Path,
			)
		}
	}

	sb.WriteString("\n")
	sb.WriteString(memoryIndexEndMarker)
	sb.WriteString("\n")

	return ms.WriteLongTerm(sb.String())
}

func (ms *MemoryStore) readManualMemoryPrefixLocked() string {
	data, err := os.ReadFile(ms.memoryFile)
	if err != nil {
		return ""
	}
	content := string(data)
	start := strings.Index(content, memoryIndexStartMarker)
	if start < 0 {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(content[:start])
}
