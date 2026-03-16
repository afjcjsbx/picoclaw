package tree

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// SessionManager manages a single JSONL session file with tree-structured entries.
// Entries form a tree via id/parentId references. The "leaf" pointer tracks the
// current position in the tree. All writes are append-only.
type SessionManager struct {
	mu sync.RWMutex

	cwd         string
	sessionDir  string
	sessionID   string
	sessionFile string
	persist     bool

	fileEntries []json.RawMessage // raw JSON lines (header + entries)
	header      *SessionHeader
	entries     []*SessionEntry          // all entries (excludes header)
	byID        map[string]*SessionEntry // index by entry ID
	labelsById  map[string]string        // resolved labels
	leafID      *string                  // current leaf entry ID (nil = before first entry)
	flushed     bool                     // whether file has been written to disk
}

// maxLineSize for scanning JSONL files (10 MB, matching memory package).
const maxLineSize = 10 * 1024 * 1024

// Create creates a new persisted session.
func Create(cwd string, sessionDir ...string) *SessionManager {
	dir := defaultSessionDir(cwd)
	if len(sessionDir) > 0 && sessionDir[0] != "" {
		dir = sessionDir[0]
	}
	sm := &SessionManager{
		cwd:        cwd,
		sessionDir: dir,
		persist:    true,
		byID:       make(map[string]*SessionEntry),
		labelsById: make(map[string]string),
	}
	sm.newSession(nil)
	return sm
}

// Open opens an existing session file.
func Open(path string, sessionDir ...string) (*SessionManager, error) {
	dir := filepath.Dir(path)
	if len(sessionDir) > 0 && sessionDir[0] != "" {
		dir = sessionDir[0]
	}

	sm := &SessionManager{
		sessionDir:  dir,
		sessionFile: filepath.Clean(path),
		persist:     true,
		byID:        make(map[string]*SessionEntry),
		labelsById:  make(map[string]string),
	}

	if err := sm.loadFromFile(path); err != nil {
		return nil, fmt.Errorf("tree: open session: %w", err)
	}

	return sm, nil
}

// ContinueRecent opens the most recent session or creates a new one.
func ContinueRecent(cwd string, sessionDir ...string) *SessionManager {
	dir := defaultSessionDir(cwd)
	if len(sessionDir) > 0 && sessionDir[0] != "" {
		dir = sessionDir[0]
	}

	mostRecent := findMostRecentSession(dir)
	if mostRecent != "" {
		sm, err := Open(mostRecent, dir)
		if err == nil {
			return sm
		}
	}

	return Create(cwd, dir)
}

// InMemory creates an in-memory session (no file persistence).
func InMemory(cwd ...string) *SessionManager {
	c := ""
	if len(cwd) > 0 {
		c = cwd[0]
	}
	sm := &SessionManager{
		cwd:        c,
		persist:    false,
		byID:       make(map[string]*SessionEntry),
		labelsById: make(map[string]string),
	}
	sm.newSession(nil)
	return sm
}

// newSession initializes a fresh session.
func (sm *SessionManager) newSession(parentSession *string) string {
	sm.sessionID = uuid.New().String()
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	sm.header = &SessionHeader{
		Type:      "session",
		Version:   CurrentSessionVersion,
		ID:        sm.sessionID,
		Timestamp: ts,
		Cwd:       sm.cwd,
	}
	if parentSession != nil {
		sm.header.ParentSession = *parentSession
	}

	headerJSON, _ := json.Marshal(sm.header)
	sm.fileEntries = []json.RawMessage{headerJSON}
	sm.entries = nil
	sm.byID = make(map[string]*SessionEntry)
	sm.labelsById = make(map[string]string)
	sm.leafID = nil
	sm.flushed = false

	if sm.persist {
		fileTs := strings.NewReplacer(":", "-", ".", "-").Replace(ts)
		os.MkdirAll(sm.sessionDir, 0o755)
		sm.sessionFile = filepath.Join(sm.sessionDir, fmt.Sprintf("%s_%s.jsonl", fileTs, sm.sessionID))
	}

	return sm.sessionFile
}

// NewSession starts a new session, optionally linked to a parent.
func (sm *SessionManager) NewSession(parentSession ...string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var ps *string
	if len(parentSession) > 0 && parentSession[0] != "" {
		ps = &parentSession[0]
	}
	return sm.newSession(ps)
}

// loadFromFile reads and indexes a JSONL session file.
func (sm *SessionManager) loadFromFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	sm.fileEntries = nil
	sm.entries = nil
	sm.byID = make(map[string]*SessionEntry)
	sm.labelsById = make(map[string]string)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		raw := make(json.RawMessage, len(line))
		copy(raw, line)
		sm.fileEntries = append(sm.fileEntries, raw)

		// Peek at type field
		var peek struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &peek) != nil {
			continue
		}

		if peek.Type == "session" {
			var h SessionHeader
			if json.Unmarshal(line, &h) == nil {
				sm.header = &h
				sm.sessionID = h.ID
				sm.cwd = h.Cwd
			}
			continue
		}

		var entry SessionEntry
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		entry.Raw = raw

		sm.entries = append(sm.entries, &entry)
		sm.byID[entry.ID] = &entry

		// Track labels
		if entry.Type == "label" && entry.TargetID != "" {
			if entry.Label != nil && *entry.Label != "" {
				sm.labelsById[entry.TargetID] = *entry.Label
			} else {
				delete(sm.labelsById, entry.TargetID)
			}
		}
	}

	if scanner.Err() != nil {
		return fmt.Errorf("tree: scan: %w", scanner.Err())
	}

	if sm.header == nil {
		return fmt.Errorf("tree: no session header found")
	}

	// Run migrations if needed
	version := sm.header.Version
	if version == 0 {
		version = 1
	}
	if version < CurrentSessionVersion {
		sm.migrateToCurrentVersion(version)
	}

	// Set leaf to last entry
	if len(sm.entries) > 0 {
		lastID := sm.entries[len(sm.entries)-1].ID
		sm.leafID = &lastID
	}

	sm.flushed = true

	// Log tree structure summary
	branchPoints := 0
	childCount := make(map[string]int)
	for _, e := range sm.entries {
		if e.ParentID != nil {
			childCount[*e.ParentID]++
		}
	}
	for _, c := range childCount {
		if c > 1 {
			branchPoints++
		}
	}
	maxDepth := 0
	for _, e := range sm.entries {
		d := sm.depthOf(e)
		if d > maxDepth {
			maxDepth = d
		}
	}

	logger.DebugCF("session", "Session loaded", map[string]any{
		"session_id":    sm.sessionID,
		"file":          filepath.Base(path),
		"version":       sm.header.Version,
		"entries":       len(sm.entries),
		"branch_points": branchPoints,
		"max_depth":     maxDepth,
	})

	return nil
}

// migrateToCurrentVersion applies migrations in place.
func (sm *SessionManager) migrateToCurrentVersion(fromVersion int) {
	if fromVersion < 2 {
		// v1 → v2: add id/parentId
		var prevID *string
		for _, entry := range sm.entries {
			if entry.ID == "" {
				entry.ID = generateID(sm.byID)
				sm.byID[entry.ID] = entry
			}
			entry.ParentID = prevID
			id := entry.ID
			prevID = &id
		}
		sm.header.Version = 2
	}

	if fromVersion < 3 {
		// v2 → v3: rename hookMessage role to custom
		for _, entry := range sm.entries {
			if entry.Type == "message" && entry.Message != nil && entry.Message.Role == "hookMessage" {
				entry.Message.Role = "custom"
			}
		}
		sm.header.Version = 3
	}

	sm.rewriteFile()
}

// --- Write Methods ---

// AppendMessage appends a conversation message. Returns the entry ID.
func (sm *SessionManager) AppendMessage(msg AgentMessage) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:      "message",
		ID:        generateID(sm.byID),
		ParentID:  sm.leafID,
		Timestamp: nowISO(),
		Message:   &msg,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendProviderMessage appends a providers.Message, converting it to AgentMessage.
func (sm *SessionManager) AppendProviderMessage(msg providers.Message) string {
	am := providerMsgToAgentMsg(msg)
	return sm.AppendMessage(am)
}

// AppendThinkingLevelChange records a thinking level change. Returns the entry ID.
func (sm *SessionManager) AppendThinkingLevelChange(thinkingLevel string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:          "thinking_level_change",
		ID:            generateID(sm.byID),
		ParentID:      sm.leafID,
		Timestamp:     nowISO(),
		ThinkingLevel: thinkingLevel,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendModelChange records a model change. Returns the entry ID.
func (sm *SessionManager) AppendModelChange(provider, modelID string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:      "model_change",
		ID:        generateID(sm.byID),
		ParentID:  sm.leafID,
		Timestamp: nowISO(),
		Provider:  provider,
		ModelID:   modelID,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCompaction records a context compaction. Returns the entry ID.
func (sm *SessionManager) AppendCompaction(
	summary, firstKeptEntryID string,
	tokensBefore int,
	details json.RawMessage,
	fromHook bool,
) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:             "compaction",
		ID:               generateID(sm.byID),
		ParentID:         sm.leafID,
		Timestamp:        nowISO(),
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     tokensBefore,
		Details:          details,
		FromHook:         fromHook,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCustomEntry appends a non-LLM-context extension entry. Returns the entry ID.
func (sm *SessionManager) AppendCustomEntry(customType string, data json.RawMessage) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:       "custom",
		ID:         generateID(sm.byID),
		ParentID:   sm.leafID,
		Timestamp:  nowISO(),
		CustomType: customType,
		Data:       data,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendCustomMessageEntry appends an LLM-context extension message. Returns the entry ID.
func (sm *SessionManager) AppendCustomMessageEntry(
	customType, content string,
	display bool,
	details json.RawMessage,
) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:       "custom_message",
		ID:         generateID(sm.byID),
		ParentID:   sm.leafID,
		Timestamp:  nowISO(),
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendSessionInfo sets the display name. Returns the entry ID.
func (sm *SessionManager) AppendSessionInfo(name string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	entry := &SessionEntry{
		Type:      "session_info",
		ID:        generateID(sm.byID),
		ParentID:  sm.leafID,
		Timestamp: nowISO(),
		Name:      strings.TrimSpace(name),
	}
	sm.appendEntry(entry)
	return entry.ID
}

// AppendLabelChange sets or clears a label on an entry. Returns the label entry ID.
func (sm *SessionManager) AppendLabelChange(targetID string, label *string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.byID[targetID]; !ok {
		return ""
	}

	entry := &SessionEntry{
		Type:      "label",
		ID:        generateID(sm.byID),
		ParentID:  sm.leafID,
		Timestamp: nowISO(),
		TargetID:  targetID,
		Label:     label,
	}
	sm.appendEntry(entry)

	if label != nil && *label != "" {
		sm.labelsById[targetID] = *label
	} else {
		delete(sm.labelsById, targetID)
	}

	return entry.ID
}

// appendEntry is the shared append logic.
func (sm *SessionManager) appendEntry(entry *SessionEntry) {
	sm.entries = append(sm.entries, entry)
	sm.byID[entry.ID] = entry

	prevLeaf := "<nil>"
	if sm.leafID != nil {
		prevLeaf = *sm.leafID
	}
	sm.leafID = &entry.ID

	raw, _ := json.Marshal(entry)
	entry.Raw = raw
	sm.fileEntries = append(sm.fileEntries, raw)

	parentStr := "<nil>"
	if entry.ParentID != nil {
		parentStr = *entry.ParentID
	}

	fields := map[string]any{
		"entry_id":   entry.ID,
		"type":       entry.Type,
		"parent_id":  parentStr,
		"prev_leaf":  prevLeaf,
		"new_leaf":   entry.ID,
		"tree_depth": sm.depthOf(entry),
		"total":      len(sm.entries),
	}
	switch entry.Type {
	case "message":
		if entry.Message != nil {
			fields["role"] = entry.Message.Role
			fields["content_len"] = len(entry.Message.Content)
		}
	case "compaction":
		fields["first_kept"] = entry.FirstKeptEntryID
		fields["tokens_before"] = entry.TokensBefore
	case "branch_summary":
		fields["from_id"] = entry.FromID
	case "label":
		fields["target_id"] = entry.TargetID
	case "session_info":
		fields["name"] = entry.Name
	}
	logger.DebugCF("session", "Tree entry appended", fields)

	sm.persistEntry(entry)
}

// persistEntry writes to disk.
func (sm *SessionManager) persistEntry(entry *SessionEntry) {
	if !sm.persist || sm.sessionFile == "" {
		return
	}

	// Lazy persistence: don't create the file until the first assistant message.
	hasAssistant := false
	for _, e := range sm.entries {
		if e.Type == "message" && e.Message != nil && e.Message.Role == "assistant" {
			hasAssistant = true
			break
		}
	}
	if !hasAssistant {
		sm.flushed = false
		return
	}

	if !sm.flushed {
		// Flush all entries at once
		os.MkdirAll(filepath.Dir(sm.sessionFile), 0o755)
		f, err := os.OpenFile(sm.sessionFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return
		}
		for _, raw := range sm.fileEntries {
			f.Write(raw)
			f.Write([]byte{'\n'})
		}
		f.Sync()
		f.Close()
		sm.flushed = true

		logger.DebugCF("session", "Session file created (first flush)", map[string]any{
			"file":    filepath.Base(sm.sessionFile),
			"entries": len(sm.entries),
		})
	} else {
		// Append single entry
		f, err := os.OpenFile(sm.sessionFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		f.Write(entry.Raw)
		f.Write([]byte{'\n'})
		f.Sync()
		f.Close()
	}
}

// rewriteFile atomically rewrites the entire session file.
func (sm *SessionManager) rewriteFile() {
	if !sm.persist || sm.sessionFile == "" {
		return
	}

	os.MkdirAll(filepath.Dir(sm.sessionFile), 0o755)

	// Rebuild fileEntries from header + entries
	sm.fileEntries = nil
	headerJSON, _ := json.Marshal(sm.header)
	sm.fileEntries = append(sm.fileEntries, headerJSON)
	for _, e := range sm.entries {
		raw, _ := json.Marshal(e)
		e.Raw = raw
		sm.fileEntries = append(sm.fileEntries, raw)
	}

	f, err := os.OpenFile(sm.sessionFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	for _, raw := range sm.fileEntries {
		f.Write(raw)
		f.Write([]byte{'\n'})
	}
	f.Sync()
	f.Close()
	sm.flushed = true
}

// --- Read Methods (Tree Traversal) ---

// GetLeafID returns the ID of the current leaf entry, or nil.
func (sm *SessionManager) GetLeafID() *string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.leafID
}

// GetLeafEntry returns the current leaf entry.
func (sm *SessionManager) GetLeafEntry() *SessionEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.leafID == nil {
		return nil
	}
	return sm.byID[*sm.leafID]
}

// GetEntry returns an entry by ID.
func (sm *SessionManager) GetEntry(id string) *SessionEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.byID[id]
}

// GetBranch returns the path from root to the given entry (or current leaf).
func (sm *SessionManager) GetBranch(fromID ...string) []*SessionEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var startID *string
	if len(fromID) > 0 && fromID[0] != "" {
		startID = &fromID[0]
	} else {
		startID = sm.leafID
	}

	return sm.getBranchLocked(startID)
}

func (sm *SessionManager) getBranchLocked(startID *string) []*SessionEntry {
	if startID == nil {
		return nil
	}

	var path []*SessionEntry
	current := sm.byID[*startID]
	for current != nil {
		path = append([]*SessionEntry{current}, path...)
		if current.ParentID == nil {
			break
		}
		current = sm.byID[*current.ParentID]
	}
	return path
}

// GetTree returns the full tree structure.
func (sm *SessionManager) GetTree() []*SessionTreeNode {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	nodeMap := make(map[string]*SessionTreeNode, len(sm.entries))
	var roots []*SessionTreeNode

	// Create nodes
	for _, entry := range sm.entries {
		label := sm.labelsById[entry.ID]
		nodeMap[entry.ID] = &SessionTreeNode{
			Entry: entry,
			Label: label,
		}
	}

	// Build tree
	for _, entry := range sm.entries {
		node := nodeMap[entry.ID]
		if entry.ParentID == nil || *entry.ParentID == entry.ID {
			roots = append(roots, node)
		} else {
			parent, ok := nodeMap[*entry.ParentID]
			if ok {
				parent.Children = append(parent.Children, node)
			} else {
				roots = append(roots, node)
			}
		}
	}

	// Sort children by timestamp
	var stack []*SessionTreeNode
	stack = append(stack, roots...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sort.Slice(node.Children, func(i, j int) bool {
			return node.Children[i].Entry.Timestamp < node.Children[j].Entry.Timestamp
		})
		stack = append(stack, node.Children...)
	}

	return roots
}

// GetChildren returns direct children of an entry.
func (sm *SessionManager) GetChildren(parentID string) []*SessionEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var children []*SessionEntry
	for _, entry := range sm.entries {
		if entry.ParentID != nil && *entry.ParentID == parentID {
			children = append(children, entry)
		}
	}
	return children
}

// GetLabel returns the label for an entry.
func (sm *SessionManager) GetLabel(id string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.labelsById[id]
}

// GetEntries returns all entries (excludes header).
func (sm *SessionManager) GetEntries() []*SessionEntry {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*SessionEntry, len(sm.entries))
	copy(result, sm.entries)
	return result
}

// GetHeader returns the session header.
func (sm *SessionManager) GetHeader() *SessionHeader {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.header
}

// GetSessionName returns the display name from the latest session_info entry.
func (sm *SessionManager) GetSessionName() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for i := len(sm.entries) - 1; i >= 0; i-- {
		if sm.entries[i].Type == "session_info" && sm.entries[i].Name != "" {
			return sm.entries[i].Name
		}
	}
	return ""
}

// GetCwd returns the working directory.
func (sm *SessionManager) GetCwd() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.cwd
}

// GetSessionDir returns the session directory.
func (sm *SessionManager) GetSessionDir() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessionDir
}

// GetSessionID returns the session UUID.
func (sm *SessionManager) GetSessionID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessionID
}

// GetSessionFile returns the session file path.
func (sm *SessionManager) GetSessionFile() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessionFile
}

// IsPersisted returns true if the session writes to disk.
func (sm *SessionManager) IsPersisted() bool {
	return sm.persist
}

// --- Branching ---

// Branch moves the leaf pointer to an earlier entry.
func (sm *SessionManager) Branch(branchFromID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.byID[branchFromID]; !ok {
		return fmt.Errorf("tree: entry %s not found", branchFromID)
	}

	prevLeaf := "<nil>"
	if sm.leafID != nil {
		prevLeaf = *sm.leafID
	}
	sm.leafID = &branchFromID

	children := 0
	for _, e := range sm.entries {
		if e.ParentID != nil && *e.ParentID == branchFromID {
			children++
		}
	}

	logger.DebugCF("session", "Branch created", map[string]any{
		"from_id":           branchFromID,
		"prev_leaf":         prevLeaf,
		"existing_children": children,
		"depth":             sm.depthOf(sm.byID[branchFromID]),
	})
	return nil
}

// ResetLeaf resets the leaf to nil (before first entry).
func (sm *SessionManager) ResetLeaf() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.leafID = nil
}

// BranchWithSummary branches and appends a branch_summary entry.
func (sm *SessionManager) BranchWithSummary(
	branchFromID *string,
	summary string,
	details json.RawMessage,
	fromHook bool,
) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if branchFromID != nil {
		if _, ok := sm.byID[*branchFromID]; !ok {
			return ""
		}
	}
	sm.leafID = branchFromID

	fromID := "root"
	if branchFromID != nil {
		fromID = *branchFromID
	}

	entry := &SessionEntry{
		Type:      "branch_summary",
		ID:        generateID(sm.byID),
		ParentID:  branchFromID,
		Timestamp: nowISO(),
		FromID:    fromID,
		Summary:   summary,
		Details:   details,
		FromHook:  fromHook,
	}
	sm.appendEntry(entry)

	summaryPreview := summary
	if len(summaryPreview) > 80 {
		summaryPreview = summaryPreview[:80] + "..."
	}
	logger.DebugCF("session", "Branch with summary", map[string]any{
		"from_id":    fromID,
		"summary_id": entry.ID,
		"summary":    summaryPreview,
		"from_hook":  fromHook,
	})

	return entry.ID
}

// CreateBranchedSession extracts the branch to a new session file.
func (sm *SessionManager) CreateBranchedSession(leafID string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	path := sm.getBranchLocked(&leafID)
	if len(path) == 0 {
		return "", fmt.Errorf("tree: entry %s not found", leafID)
	}

	newSessionID := uuid.New().String()
	ts := nowISO()
	fileTs := strings.NewReplacer(":", "-", ".", "-").Replace(ts)
	newSessionFile := filepath.Join(sm.sessionDir, fmt.Sprintf("%s_%s.jsonl", fileTs, newSessionID))

	newHeader := &SessionHeader{
		Type:      "session",
		Version:   CurrentSessionVersion,
		ID:        newSessionID,
		Timestamp: ts,
		Cwd:       sm.cwd,
	}
	if sm.persist && sm.sessionFile != "" {
		newHeader.ParentSession = sm.sessionFile
	}

	// Filter out labels from path; we'll recreate them
	var pathWithoutLabels []*SessionEntry
	for _, e := range path {
		if e.Type != "label" {
			pathWithoutLabels = append(pathWithoutLabels, e)
		}
	}

	// Collect labels for entries in path
	pathIDs := make(map[string]bool, len(pathWithoutLabels))
	for _, e := range pathWithoutLabels {
		pathIDs[e.ID] = true
	}

	if sm.persist {
		os.MkdirAll(filepath.Dir(newSessionFile), 0o755)
		f, err := os.OpenFile(newSessionFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return "", fmt.Errorf("tree: create branched session: %w", err)
		}

		headerJSON, _ := json.Marshal(newHeader)
		f.Write(headerJSON)
		f.Write([]byte{'\n'})

		for _, e := range pathWithoutLabels {
			raw, _ := json.Marshal(e)
			f.Write(raw)
			f.Write([]byte{'\n'})
		}

		// Write label entries for entries in the path
		for targetID, label := range sm.labelsById {
			if pathIDs[targetID] {
				labelEntry := &SessionEntry{
					Type:      "label",
					ID:        generateIDFromSet(pathIDs),
					ParentID:  &pathWithoutLabels[len(pathWithoutLabels)-1].ID,
					Timestamp: nowISO(),
					TargetID:  targetID,
					Label:     &label,
				}
				raw, _ := json.Marshal(labelEntry)
				f.Write(raw)
				f.Write([]byte{'\n'})
			}
		}

		f.Sync()
		f.Close()

		// Update current manager to point to new file
		sm.sessionID = newSessionID
		sm.sessionFile = newSessionFile
		sm.header = newHeader
		sm.flushed = true

		return newSessionFile, nil
	}

	// In-memory mode
	sm.sessionID = newSessionID
	sm.header = newHeader
	sm.entries = pathWithoutLabels
	sm.rebuildIndex()
	return "", nil
}

// SetSessionFile switches to a different session file.
func (sm *SessionManager) SetSessionFile(path string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	abs, _ := filepath.Abs(path)
	sm.sessionFile = abs

	if _, err := os.Stat(abs); err == nil {
		return sm.loadFromFile(abs)
	}

	sm.newSession(nil)
	sm.sessionFile = abs
	return nil
}

// BuildSessionContext builds the LLM context from the current tree position.
func (sm *SessionManager) BuildSessionContext() SessionContext {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	ctx := buildSessionContext(sm.entries, sm.leafID, sm.byID)

	leafStr := "<nil>"
	if sm.leafID != nil {
		leafStr = *sm.leafID
	}

	// Count messages by role
	roleCounts := make(map[string]int)
	for _, m := range ctx.Messages {
		roleCounts[m.Role]++
	}

	logger.DebugCF("session", "Context built from tree", map[string]any{
		"leaf_id":        leafStr,
		"path_messages":  len(ctx.Messages),
		"thinking_level": ctx.ThinkingLevel,
		"has_model":      ctx.Model != nil,
		"roles":          roleCounts,
	})

	return ctx
}

// rebuildIndex rebuilds the byID and labelsById maps from entries.
func (sm *SessionManager) rebuildIndex() {
	sm.byID = make(map[string]*SessionEntry, len(sm.entries))
	sm.labelsById = make(map[string]string)

	for _, e := range sm.entries {
		sm.byID[e.ID] = e
		if e.Type == "label" && e.TargetID != "" {
			if e.Label != nil && *e.Label != "" {
				sm.labelsById[e.TargetID] = *e.Label
			} else {
				delete(sm.labelsById, e.TargetID)
			}
		}
	}

	if len(sm.entries) > 0 {
		lastID := sm.entries[len(sm.entries)-1].ID
		sm.leafID = &lastID
	} else {
		sm.leafID = nil
	}
}

// --- Static Listing ---

// List returns session info for all sessions in a directory.
func List(cwd string, sessionDir ...string) ([]SessionInfo, error) {
	dir := defaultSessionDir(cwd)
	if len(sessionDir) > 0 && sessionDir[0] != "" {
		dir = sessionDir[0]
	}

	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionInfo
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, err := buildSessionInfo(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		sessions = append(sessions, info)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified > sessions[j].Modified
	})

	return sessions, nil
}

// buildSessionInfo reads minimal info from a session file.
func buildSessionInfo(path string) (SessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer f.Close()

	info := SessionInfo{Path: path}

	fi, _ := f.Stat()
	if fi != nil {
		info.Modified = fi.ModTime().Format(time.RFC3339)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var peek struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Cwd     string `json:"cwd"`
			Ts      string `json:"timestamp"`
			Parent  string `json:"parentSession"`
			Name    string `json:"name"`
			Message *struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &peek) != nil {
			continue
		}

		switch peek.Type {
		case "session":
			info.ID = peek.ID
			info.Cwd = peek.Cwd
			info.Created = peek.Ts
			info.ParentSessionPath = peek.Parent
		case "message":
			info.MessageCount++
			if peek.Message != nil && peek.Message.Role == "user" && info.FirstMessage == "" {
				info.FirstMessage = peek.Message.Content
			}
		case "session_info":
			if peek.Name != "" {
				info.Name = peek.Name
			}
		}
	}

	return info, nil
}

// --- Tree visualization helpers ---

// depthOf returns the depth of an entry in the tree (0 = root).
func (sm *SessionManager) depthOf(entry *SessionEntry) int {
	depth := 0
	current := entry
	for current != nil && current.ParentID != nil {
		depth++
		current = sm.byID[*current.ParentID]
	}
	return depth
}

// LogTree logs a visual ASCII representation of the current session tree.
// Useful for debugging conversation flow. Shows entry types, roles,
// content previews, branch points, and the current leaf position.
func (sm *SessionManager) LogTree() {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	tree := sm.getTreeLocked()
	if len(tree) == 0 {
		logger.DebugCF("session", "Tree is empty", nil)
		return
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Session %s (entries: %d)", sm.sessionID[:8], len(sm.entries)))

	for _, root := range tree {
		sm.renderNode(&lines, root, "", true)
	}

	logger.DebugCF("session", "Session tree:\n"+strings.Join(lines, "\n"), map[string]any{
		"session_id": sm.sessionID[:8],
		"entries":    len(sm.entries),
	})
}

// getTreeLocked builds the tree without acquiring a lock (caller must hold it).
func (sm *SessionManager) getTreeLocked() []*SessionTreeNode {
	nodeMap := make(map[string]*SessionTreeNode, len(sm.entries))
	var roots []*SessionTreeNode

	for _, entry := range sm.entries {
		label := sm.labelsById[entry.ID]
		nodeMap[entry.ID] = &SessionTreeNode{
			Entry: entry,
			Label: label,
		}
	}

	for _, entry := range sm.entries {
		node := nodeMap[entry.ID]
		if entry.ParentID == nil || *entry.ParentID == entry.ID {
			roots = append(roots, node)
		} else {
			parent, ok := nodeMap[*entry.ParentID]
			if ok {
				parent.Children = append(parent.Children, node)
			} else {
				roots = append(roots, node)
			}
		}
	}

	var stack []*SessionTreeNode
	stack = append(stack, roots...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sort.Slice(node.Children, func(i, j int) bool {
			return node.Children[i].Entry.Timestamp < node.Children[j].Entry.Timestamp
		})
		stack = append(stack, node.Children...)
	}

	return roots
}

// renderNode renders a tree node as ASCII art.
func (sm *SessionManager) renderNode(lines *[]string, node *SessionTreeNode, prefix string, isLast bool) {
	connector := "├── "
	if isLast {
		connector = "└── "
	}

	entry := node.Entry
	isLeaf := sm.leafID != nil && *sm.leafID == entry.ID
	leafMarker := ""
	if isLeaf {
		leafMarker = " ◀ LEAF"
	}
	labelMarker := ""
	if node.Label != "" {
		labelMarker = fmt.Sprintf(" [%s]", node.Label)
	}

	desc := sm.entryDescription(entry)
	line := fmt.Sprintf("%s%s%s (%s)%s%s", prefix, connector, entry.ID[:4], desc, labelMarker, leafMarker)
	*lines = append(*lines, line)

	childPrefix := prefix + "│   "
	if isLast {
		childPrefix = prefix + "    "
	}

	for i, child := range node.Children {
		sm.renderNode(lines, child, childPrefix, i == len(node.Children)-1)
	}
}

// entryDescription returns a short human-readable description of an entry.
func (sm *SessionManager) entryDescription(entry *SessionEntry) string {
	switch entry.Type {
	case "message":
		if entry.Message == nil {
			return "message: <nil>"
		}
		content := entry.Message.Content
		if len(content) > 60 {
			content = content[:60] + "..."
		}
		content = strings.ReplaceAll(content, "\n", "\\n")
		return fmt.Sprintf("%s: %q", entry.Message.Role, content)
	case "thinking_level_change":
		return fmt.Sprintf("thinking: %s", entry.ThinkingLevel)
	case "model_change":
		return fmt.Sprintf("model: %s/%s", entry.Provider, entry.ModelID)
	case "compaction":
		return fmt.Sprintf("compaction: %d tokens, keep from %s", entry.TokensBefore, entry.FirstKeptEntryID[:4])
	case "branch_summary":
		summary := entry.Summary
		if len(summary) > 50 {
			summary = summary[:50] + "..."
		}
		return fmt.Sprintf("branch_summary: %q", summary)
	case "custom":
		return fmt.Sprintf("custom(%s)", entry.CustomType)
	case "custom_message":
		content := entry.Content
		if len(content) > 50 {
			content = content[:50] + "..."
		}
		return fmt.Sprintf("custom_msg(%s): %q", entry.CustomType, content)
	case "label":
		if entry.Label != nil {
			return fmt.Sprintf("label: %s -> %q", entry.TargetID[:4], *entry.Label)
		}
		return fmt.Sprintf("label: %s -> (cleared)", entry.TargetID[:4])
	case "session_info":
		return fmt.Sprintf("info: name=%q", entry.Name)
	default:
		return entry.Type
	}
}

// --- Helpers ---

func defaultSessionDir(cwd string) string {
	safe := strings.ReplaceAll(cwd, string(filepath.Separator), "--")
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".picoclaw", "sessions", safe)
}

func findMostRecentSession(dir string) string {
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var best string
	var bestTime time.Time

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			best = filepath.Join(dir, f.Name())
		}
	}

	return best
}

// generateID creates a unique 8-char hex ID.
func generateID(existing map[string]*SessionEntry) string {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		id := hex.EncodeToString(b)
		if _, exists := existing[id]; !exists {
			return id
		}
	}
}

// generateIDFromSet creates a unique 8-char hex ID not in the given set.
func generateIDFromSet(existing map[string]bool) string {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		id := hex.EncodeToString(b)
		if !existing[id] {
			existing[id] = true
			return id
		}
	}
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// providerMsgToAgentMsg converts a providers.Message to AgentMessage.
func providerMsgToAgentMsg(msg providers.Message) AgentMessage {
	return AgentMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  msg.ToolCalls,
		ToolCallID: msg.ToolCallID,
		Media:      msg.Media,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// agentMsgToProviderMsg converts an AgentMessage to providers.Message.
func agentMsgToProviderMsg(msg AgentMessage) providers.Message {
	return providers.Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  msg.ToolCalls,
		ToolCallID: msg.ToolCallID,
		Media:      msg.Media,
	}
}
