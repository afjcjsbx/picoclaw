package tree

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// Compile-time check: Adapter implements SessionStore.
var _ session.SessionStore = (*Adapter)(nil)

func TestInMemorySession(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	id2 := sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	assert.NotEmpty(t, id1)
	assert.NotEmpty(t, id2)

	entries := sm.GetEntries()
	assert.Len(t, entries, 2)
	assert.Equal(t, "message", entries[0].Type)
	assert.Equal(t, "user", entries[0].Message.Role)
	assert.Equal(t, "hello", entries[0].Message.Content)
}

func TestTreeStructure(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "first"})
	id2 := sm.AppendMessage(AgentMessage{Role: "assistant", Content: "reply1"})

	// Branch from id1
	err := sm.Branch(id1)
	require.NoError(t, err)

	id3 := sm.AppendMessage(AgentMessage{Role: "user", Content: "alternate"})

	// Verify tree structure
	tree := sm.GetTree()
	require.Len(t, tree, 1) // one root

	root := tree[0]
	assert.Equal(t, id1, root.Entry.ID)
	assert.Len(t, root.Children, 2) // two branches from root

	// One child should be id2, other should be id3
	childIDs := []string{root.Children[0].Entry.ID, root.Children[1].Entry.ID}
	assert.Contains(t, childIDs, id2)
	assert.Contains(t, childIDs, id3)
}

func TestGetBranch(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "first"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "reply1"})
	sm.AppendMessage(AgentMessage{Role: "user", Content: "second"})

	// Branch from id1
	err := sm.Branch(id1)
	require.NoError(t, err)

	altID := sm.AppendMessage(AgentMessage{Role: "user", Content: "alternate"})

	// GetBranch from alternate path
	branch := sm.GetBranch(altID)
	assert.Len(t, branch, 2) // id1 -> altID
	assert.Equal(t, id1, branch[0].ID)
	assert.Equal(t, altID, branch[1].ID)
}

func TestBuildSessionContext(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi there"})
	sm.AppendMessage(AgentMessage{Role: "user", Content: "how are you?"})

	ctx := sm.BuildSessionContext()
	assert.Len(t, ctx.Messages, 3)
	assert.Equal(t, "user", ctx.Messages[0].Role)
	assert.Equal(t, "hello", ctx.Messages[0].Content)
	assert.Equal(t, "assistant", ctx.Messages[1].Role)
	assert.Equal(t, "off", ctx.ThinkingLevel)
}

func TestBuildSessionContextWithBranch(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	// Branch from id1
	err := sm.Branch(id1)
	require.NoError(t, err)

	sm.AppendMessage(AgentMessage{Role: "user", Content: "different question"})

	ctx := sm.BuildSessionContext()
	assert.Len(t, ctx.Messages, 2) // hello + different question
	assert.Equal(t, "hello", ctx.Messages[0].Content)
	assert.Equal(t, "different question", ctx.Messages[1].Content)
}

func TestCompaction(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "msg1"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "reply1"})
	id3 := sm.AppendMessage(AgentMessage{Role: "user", Content: "msg2"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "reply2"})
	sm.AppendMessage(AgentMessage{Role: "user", Content: "msg3"})

	// Add compaction that keeps from id3 onwards
	sm.AppendCompaction("Summary of msg1 and reply1", id3, 5000, nil, false)

	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "reply3"})

	ctx := sm.BuildSessionContext()

	// Should have: compactionSummary, msg2, reply2, msg3, reply3
	require.True(t, len(ctx.Messages) >= 4)
	assert.Equal(t, "compactionSummary", ctx.Messages[0].Role)
	assert.Equal(t, "Summary of msg1 and reply1", ctx.Messages[0].Summary)

	_ = id1 // used above in AppendMessage
}

func TestThinkingLevelChange(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendThinkingLevelChange("high")
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "thinking deeply"})

	ctx := sm.BuildSessionContext()
	assert.Equal(t, "high", ctx.ThinkingLevel)
}

func TestModelChange(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendModelChange("anthropic", "claude-sonnet-4-5")
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	ctx := sm.BuildSessionContext()
	require.NotNil(t, ctx.Model)
	assert.Equal(t, "anthropic", ctx.Model.Provider)
	assert.Equal(t, "claude-sonnet-4-5", ctx.Model.ModelID)
}

func TestLabels(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})

	label := "checkpoint-1"
	sm.AppendLabelChange(id1, &label)

	assert.Equal(t, "checkpoint-1", sm.GetLabel(id1))

	// Clear label
	sm.AppendLabelChange(id1, nil)
	assert.Equal(t, "", sm.GetLabel(id1))
}

func TestCustomEntry(t *testing.T) {
	sm := InMemory("/tmp/test")

	data, _ := json.Marshal(map[string]int{"count": 42})
	id := sm.AppendCustomEntry("my-extension", data)
	assert.NotEmpty(t, id)

	entry := sm.GetEntry(id)
	require.NotNil(t, entry)
	assert.Equal(t, "custom", entry.Type)
	assert.Equal(t, "my-extension", entry.CustomType)

	// Custom entries should NOT appear in context
	ctx := sm.BuildSessionContext()
	assert.Empty(t, ctx.Messages)
}

func TestCustomMessageEntry(t *testing.T) {
	sm := InMemory("/tmp/test")

	id := sm.AppendCustomMessageEntry("my-ext", "injected context", true, nil)
	assert.NotEmpty(t, id)

	// Custom message entries SHOULD appear in context
	ctx := sm.BuildSessionContext()
	require.Len(t, ctx.Messages, 1)
	assert.Equal(t, "custom", ctx.Messages[0].Role)
	assert.Equal(t, "injected context", ctx.Messages[0].Content)
}

func TestSessionInfo(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendSessionInfo("My Cool Session")
	assert.Equal(t, "My Cool Session", sm.GetSessionName())

	sm.AppendSessionInfo("Renamed Session")
	assert.Equal(t, "Renamed Session", sm.GetSessionName())
}

func TestBranchWithSummary(t *testing.T) {
	sm := InMemory("/tmp/test")

	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	bsID := sm.BranchWithSummary(&id1, "Previous branch discussed greetings", nil, false)
	assert.NotEmpty(t, bsID)

	sm.AppendMessage(AgentMessage{Role: "user", Content: "new topic"})

	ctx := sm.BuildSessionContext()
	// Should have: hello, branchSummary, new topic
	assert.Len(t, ctx.Messages, 3)
	assert.Equal(t, "branchSummary", ctx.Messages[1].Role)
	assert.Equal(t, "Previous branch discussed greetings", ctx.Messages[1].Summary)
}

func TestResetLeaf(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	sm.ResetLeaf()
	assert.Nil(t, sm.GetLeafID())

	// New entry should be a new root
	sm.AppendMessage(AgentMessage{Role: "user", Content: "new root"})

	tree := sm.GetTree()
	assert.Len(t, tree, 2) // two roots
}

func TestPersistedSession(t *testing.T) {
	dir := t.TempDir()

	// Create and write
	sm := Create("/tmp/test", dir)
	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})

	sessionFile := sm.GetSessionFile()
	assert.NotEmpty(t, sessionFile)

	// Verify file exists
	_, err := os.Stat(sessionFile)
	require.NoError(t, err)

	// Re-open and verify
	sm2, err := Open(sessionFile, dir)
	require.NoError(t, err)

	entries := sm2.GetEntries()
	assert.Len(t, entries, 2)
	assert.Equal(t, "user", entries[0].Message.Role)
	assert.Equal(t, "hello", entries[0].Message.Content)
}

func TestAdapter_SessionStoreInterface(t *testing.T) {
	dir := t.TempDir()
	adapter := NewAdapter(dir)
	defer adapter.Close()

	// AddMessage + GetHistory
	adapter.AddMessage("s1", "user", "hello")
	adapter.AddMessage("s1", "assistant", "hi")

	history := adapter.GetHistory("s1")
	assert.Len(t, history, 2)
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, "hello", history[0].Content)

	// AddFullMessage
	adapter.AddFullMessage("s2", providers.Message{
		Role:    "assistant",
		Content: "with tools",
		ToolCalls: []providers.ToolCall{
			{ID: "tc1", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`}},
		},
	})

	h2 := adapter.GetHistory("s2")
	assert.Len(t, h2, 1)

	// Session isolation
	h1 := adapter.GetHistory("s1")
	assert.Len(t, h1, 2)
	assert.Len(t, h2, 1)
}

func TestAdapter_TreeAccess(t *testing.T) {
	dir := t.TempDir()
	adapter := NewAdapter(dir)
	defer adapter.Close()

	adapter.AddMessage("s1", "user", "hello")
	adapter.AddMessage("s1", "assistant", "hi")

	// Access the underlying tree manager
	sm := adapter.Manager("s1")
	require.NotNil(t, sm)

	entries := sm.GetEntries()
	assert.Len(t, entries, 2)

	tree := sm.GetTree()
	assert.Len(t, tree, 1)
	assert.Len(t, tree[0].Children, 1)
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()

	sm1 := Create("project-a", dir)
	sm1.AppendMessage(AgentMessage{Role: "user", Content: "hello from A"})
	sm1.AppendMessage(AgentMessage{Role: "assistant", Content: "hi A"})

	sm2 := Create("project-b", dir)
	sm2.AppendMessage(AgentMessage{Role: "user", Content: "hello from B"})
	sm2.AppendMessage(AgentMessage{Role: "assistant", Content: "hi B"})

	sessions, err := List("", dir)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestMigrationFromLegacyJSON(t *testing.T) {
	legacyDir := t.TempDir()
	treeDir := filepath.Join(t.TempDir(), "tree")

	// Create a legacy JSON session
	sess := struct {
		Key      string              `json:"key"`
		Messages []providers.Message `json:"messages"`
		Summary  string              `json:"summary"`
	}{
		Key: "test-session",
		Messages: []providers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		Summary: "test summary",
	}
	data, _ := json.MarshalIndent(sess, "", "  ")
	os.WriteFile(filepath.Join(legacyDir, "test_session.json"), data, 0o644)

	n, err := MigrateFromLinear(context.Background(), legacyDir, treeDir)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Verify migration marker
	_, err = os.Stat(filepath.Join(legacyDir, "test_session.json.tree-migrated"))
	assert.NoError(t, err)

	// Verify tree sessions were created
	sessions, err := List("", treeDir)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

func TestGetChildren(t *testing.T) {
	sm := InMemory("/tmp/test")

	parentID := sm.AppendMessage(AgentMessage{Role: "user", Content: "parent"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "child1"})

	// Branch from parent
	err := sm.Branch(parentID)
	require.NoError(t, err)
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "child2"})

	children := sm.GetChildren(parentID)
	assert.Len(t, children, 2)
}

func TestCreateBranchedSession(t *testing.T) {
	dir := t.TempDir()
	sm := Create("/tmp/test", dir)

	sm.AppendMessage(AgentMessage{Role: "user", Content: "hello"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "hi"})
	id3 := sm.AppendMessage(AgentMessage{Role: "user", Content: "branch target"})

	newFile, err := sm.CreateBranchedSession(id3)
	require.NoError(t, err)
	assert.NotEmpty(t, newFile)

	// Open the new session and verify
	sm2, err := Open(newFile, dir)
	require.NoError(t, err)

	entries := sm2.GetEntries()
	assert.Len(t, entries, 3)
}

func TestLogTree(t *testing.T) {
	sm := InMemory("/tmp/test")

	// Build a conversation with branches, compaction, labels
	id1 := sm.AppendMessage(AgentMessage{Role: "user", Content: "Help me build a REST API in Go"})
	id2 := sm.AppendMessage(
		AgentMessage{Role: "assistant", Content: "Sure! Let's start with the router. I recommend using chi..."},
	)
	sm.AppendMessage(AgentMessage{Role: "user", Content: "Add authentication"})
	id4 := sm.AppendMessage(AgentMessage{Role: "assistant", Content: "Here's JWT auth middleware for chi..."})

	// Label the auth completion
	label := "auth-done"
	sm.AppendLabelChange(id4, &label)

	// Branch from id2 to explore different approach
	sm.BranchWithSummary(&id2, "Previous branch: added JWT auth middleware", nil, false)
	sm.AppendMessage(AgentMessage{Role: "user", Content: "Actually, use session-based auth instead"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "OK, here's session-based auth with Redis..."})

	// Branch from root to start fresh topic
	err := sm.Branch(id1)
	require.NoError(t, err)
	sm.AppendMessage(AgentMessage{Role: "user", Content: "Wait, let me rethink the architecture"})

	// Verify tree structure
	treeNodes := sm.GetTree()
	require.Len(t, treeNodes, 1)

	root := treeNodes[0]
	assert.Equal(t, id1, root.Entry.ID)

	// Root should have 2 children: id2 (original branch) and "Wait, let me rethink"
	// The BranchWithSummary is a child of id2, not id1
	assert.Len(t, root.Children, 2)

	// Test visual rendering (exercise the code path)
	sm.LogTree()

	// Also test entryDescription for coverage
	sm.mu.RLock()
	for _, e := range sm.entries {
		desc := sm.entryDescription(e)
		assert.NotEmpty(t, desc, "entry %s (%s) should have description", e.ID, e.Type)
	}
	sm.mu.RUnlock()
}

func TestLogTreeEmpty(t *testing.T) {
	sm := InMemory("/tmp/test")
	sm.LogTree() // should not panic on empty tree
}

func TestDepthOf(t *testing.T) {
	sm := InMemory("/tmp/test")

	sm.AppendMessage(AgentMessage{Role: "user", Content: "depth 0"})
	sm.AppendMessage(AgentMessage{Role: "assistant", Content: "depth 1"})
	sm.AppendMessage(AgentMessage{Role: "user", Content: "depth 2"})

	entries := sm.GetEntries()
	sm.mu.RLock()
	assert.Equal(t, 0, sm.depthOf(entries[0]))
	assert.Equal(t, 1, sm.depthOf(entries[1]))
	assert.Equal(t, 2, sm.depthOf(entries[2]))
	sm.mu.RUnlock()
}
