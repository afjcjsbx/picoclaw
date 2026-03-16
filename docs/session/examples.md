# Tree Session Examples

Practical examples of tree session operations.

---

## 1. Basic conversation

```go
sm := tree.InMemory()

sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "What is Go?"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "Go is a programming language..."})
sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "Show me an example"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "```go\npackage main\n..."})
```

Tree:
```
[user: "What is Go?"]
    |
    [assistant: "Go is a..."]
        |
        [user: "Show me an example"]
            |
            [assistant: "package main..."]  <-- leaf
```

Context sent to LLM: all 4 messages in order.

---

## 2. Branching

```go
sm := tree.InMemory()

id1 := sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "Write a function"})
id2 := sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "func add(a, b int) int { ... }"})

// User didn't like the answer, branch from their original message
sm.Branch(id1)
sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "Write a generic function"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "func add[T Numeric](a, b T) T { ... }"})
```

Tree:
```
[user: "Write a function"]
    |
    +-- [assistant: "func add(a, b int)..."]     <-- abandoned branch
    |
    +-- [user: "Write a generic function"]
            |
            [assistant: "func add[T]..."]         <-- current leaf
```

Context sent to LLM: `"Write a function"` -> `"Write a generic function"` -> `"func add[T]..."`.
The abandoned branch is not included.

---

## 3. Branch with summary

Preserve context from the abandoned branch:

```go
// Branch from id1 with a summary of what happened in the abandoned branch
sm.BranchWithSummary(&id1, "Previous branch: assistant wrote a non-generic add function. User wanted generics.", nil, false)
sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "Now write it with constraints"})
```

Tree:
```
[user: "Write a function"]
    |
    +-- [assistant: "func add(a, b int)..."]     <-- abandoned
    |
    +-- [branch_summary: "Previous branch: ..."]
            |
            [user: "Now write it with constraints"]  <-- leaf
```

Context sent to LLM:
1. `"Write a function"` (original user message)
2. `branchSummary: "Previous branch: assistant wrote..."` (context from abandoned branch)
3. `"Now write it with constraints"` (new user message)

---

## 4. Compaction

When the context window gets too large:

```go
// Session has many messages already...
entries := sm.GetEntries()

// Find the entry to keep from (e.g. last 10 messages)
var msgEntries []*tree.SessionEntry
for _, e := range entries {
    if e.Type == "message" {
        msgEntries = append(msgEntries, e)
    }
}
firstKept := msgEntries[len(msgEntries)-10]

sm.AppendCompaction(
    "User and assistant discussed implementing a REST API. Key decisions: use chi router, PostgreSQL, JWT auth.",
    firstKept.ID,
    45000, // tokens before compaction
    nil,   // no extension details
    false, // not from hook
)
```

Context sent to LLM after compaction:
1. `compactionSummary: "User and assistant discussed..."` (summary)
2. Last 10 messages (from `firstKeptEntryId` onwards)
3. Any messages appended after the compaction entry

---

## 5. Labels (bookmarks)

```go
sm := tree.InMemory()

id1 := sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "Start of important section"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "..."})

// Bookmark the important point
label := "checkpoint-auth"
sm.AppendLabelChange(id1, &label)

// Later, retrieve it
fmt.Println(sm.GetLabel(id1)) // "checkpoint-auth"

// Labels show up in the tree
tree := sm.GetTree()
// tree[0].Label == "checkpoint-auth"

// Clear the label
sm.AppendLabelChange(id1, nil)
```

---

## 6. Custom entries (extension data)

Store extension state that survives session reloads:

```go
import "encoding/json"

// Save extension state (NOT sent to LLM)
data, _ := json.Marshal(map[string]any{
    "filesModified": []string{"main.go", "handler.go"},
    "testsPassed":   true,
})
sm.AppendCustomEntry("code-review", data)

// On reload, scan for your extension's entries
for _, entry := range sm.GetEntries() {
    if entry.Type == "custom" && entry.CustomType == "code-review" {
        var state map[string]any
        json.Unmarshal(entry.Data, &state)
        // reconstruct extension state
    }
}
```

---

## 7. Custom messages (extension context injection)

Inject content that the LLM sees:

```go
// Inject search results into context (visible in UI)
sm.AppendCustomMessageEntry(
    "web-search",                          // customType
    "Search results for 'Go generics':\n1. ...\n2. ...",  // content
    true,                                  // display in UI
    nil,                                   // no details
)

// Inject hidden context (not shown in UI, but sent to LLM)
sm.AppendCustomMessageEntry(
    "file-tracker",
    "Files currently open: main.go, handler.go",
    false, // hidden from UI
    nil,
)
```

---

## 8. Tree traversal

```go
sm := tree.InMemory()

id1 := sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "A"})
id2 := sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "B"})
sm.Branch(id1)
id3 := sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "C"})

// Get full tree
treeNodes := sm.GetTree()
// treeNodes[0].Entry.ID == id1
// treeNodes[0].Children[0].Entry.ID == id2  (first branch)
// treeNodes[0].Children[1].Entry.ID == id3  (second branch)

// Get path from root to specific entry
branch := sm.GetBranch(id2)
// branch = [entry(id1), entry(id2)]

// Get children of a node
children := sm.GetChildren(id1)
// children = [entry(id2), entry(id3)]
```

---

## 9. Persisted session lifecycle

```go
dir := "/path/to/sessions"

// Create
sm := tree.Create("/my/project", dir)
sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "hello"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "hi"})
// File written at: /path/to/sessions/2026-03-14T10-00-00-..._<uuid>.jsonl

file := sm.GetSessionFile()

// Later: reopen
sm2, _ := tree.Open(file, dir)
entries := sm2.GetEntries()
// entries[0].Message.Content == "hello"

// Or: continue most recent
sm3 := tree.ContinueRecent("/my/project", dir)
// automatically opens the most recently modified .jsonl file

// List all sessions
sessions, _ := tree.List("/my/project", dir)
for _, info := range sessions {
    fmt.Printf("%s: %s (%d messages)\n", info.Name, info.FirstMessage, info.MessageCount)
}
```

---

## 10. SessionStore adapter usage

Use with the agent loop without changing any loop code:

```go
adapter := tree.NewAdapter("/path/to/sessions")

// Works like the old SessionStore
adapter.AddMessage("telegram:123:456", "user", "hello")
adapter.AddMessage("telegram:123:456", "assistant", "hi")
history := adapter.GetHistory("telegram:123:456")

// But you can also access the tree API
sm := adapter.Manager("telegram:123:456")
sm.Branch(sm.GetEntries()[0].ID) // branch the conversation
sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "actually..."})

// GetHistory now returns the new branch's context
history = adapter.GetHistory("telegram:123:456")
```

---

## 11. Extract branch to new session

```go
sm := tree.Create("/project", "/sessions")

sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "A"})
sm.AppendMessage(tree.AgentMessage{Role: "assistant", Content: "B"})
id3 := sm.AppendMessage(tree.AgentMessage{Role: "user", Content: "C"})

// Create a new session file containing only the path A -> B -> C
newFile, _ := sm.CreateBranchedSession(id3)
// newFile points to a new .jsonl with only 3 entries
// The new session's header has parentSession pointing to the original
```

---

## 12. JSONL file example

Complete example of a session file with branching and compaction:

```jsonl
{"type":"session","version":3,"id":"550e8400-e29b-41d4-a716-446655440000","timestamp":"2026-03-14T10:00:00.000000000Z","cwd":"/home/user/project"}
{"type":"message","id":"a1000001","parentId":null,"timestamp":"2026-03-14T10:00:01.000000000Z","message":{"role":"user","content":"Help me write a REST API","timestamp":1710410401000}}
{"type":"message","id":"a1000002","parentId":"a1000001","timestamp":"2026-03-14T10:00:05.000000000Z","message":{"role":"assistant","content":"Sure! Let's start with the router...","provider":"anthropic","model":"claude-sonnet-4-5","timestamp":1710410405000}}
{"type":"message","id":"a1000003","parentId":"a1000002","timestamp":"2026-03-14T10:00:10.000000000Z","message":{"role":"user","content":"Add authentication","timestamp":1710410410000}}
{"type":"message","id":"a1000004","parentId":"a1000003","timestamp":"2026-03-14T10:00:15.000000000Z","message":{"role":"assistant","content":"Here's JWT auth middleware...","provider":"anthropic","model":"claude-sonnet-4-5","timestamp":1710410415000}}
{"type":"compaction","id":"a1000005","parentId":"a1000004","timestamp":"2026-03-14T10:30:00.000000000Z","summary":"User asked for REST API. Assistant set up chi router with JWT auth middleware.","firstKeptEntryId":"a1000003","tokensBefore":45000}
{"type":"message","id":"a1000006","parentId":"a1000005","timestamp":"2026-03-14T10:30:05.000000000Z","message":{"role":"user","content":"Now add database","timestamp":1710412205000}}
{"type":"message","id":"a1000007","parentId":"a1000006","timestamp":"2026-03-14T10:30:10.000000000Z","message":{"role":"assistant","content":"Let's add PostgreSQL with sqlx...","provider":"anthropic","model":"claude-sonnet-4-5","timestamp":1710412210000}}
{"type":"branch_summary","id":"a1000008","parentId":"a1000004","timestamp":"2026-03-14T10:35:00.000000000Z","fromId":"a1000004","summary":"Abandoned branch: discussed adding PostgreSQL with sqlx."}
{"type":"message","id":"a1000009","parentId":"a1000008","timestamp":"2026-03-14T10:35:05.000000000Z","message":{"role":"user","content":"Actually, let's use SQLite instead","timestamp":1710412505000}}
{"type":"session_info","id":"a100000a","parentId":"a1000009","timestamp":"2026-03-14T10:35:10.000000000Z","name":"REST API project"}
{"type":"label","id":"a100000b","parentId":"a100000a","timestamp":"2026-03-14T10:35:15.000000000Z","targetId":"a1000004","label":"auth-done"}
```

This file contains:
- A linear conversation (entries 01-04)
- A compaction at entry 05 (summarizing 01-02, keeping 03 onwards)
- A continuation after compaction (entries 06-07)
- A branch from entry 04 with summary (entries 08-09)
- Session metadata (entry 0a: display name)
- A label (entry 0b: bookmark on the auth completion point)
