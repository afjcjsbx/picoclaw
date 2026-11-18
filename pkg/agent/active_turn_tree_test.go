package agent

import (
	"reflect"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestGetActiveTurnTree verifies that the tree contains the session's root turn
// followed by its running descendants, and skips children that already finished.
func TestGetActiveTurnTree(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ModelName: "gpt-4o-mini",
				Provider:  "mock",
			},
		},
	}
	al := NewAgentLoop(cfg, nil, &simpleMockProviderAPI{response: "ok"})

	sessionKey := "tree-session"
	al.activeTurnStates.Store(sessionKey, &turnState{
		turnID:       "root",
		childTurnIDs: []string{"child-1", "child-2"},
	})
	al.activeTurnStates.Store("child-1", &turnState{
		turnID:       "child-1",
		parentTurnID: "root",
		depth:        1,
		childTurnIDs: []string{"grandchild"},
	})
	al.activeTurnStates.Store("grandchild", &turnState{
		turnID:       "grandchild",
		parentTurnID: "child-1",
		depth:        2,
	})
	// "child-2" is intentionally not registered: it already finished.
	defer func() {
		for _, key := range []string{sessionKey, "child-1", "grandchild"} {
			al.activeTurnStates.Delete(key)
		}
	}()

	tree := al.GetActiveTurnTree(sessionKey)
	got := make([]string, 0, len(tree))
	for _, info := range tree {
		got = append(got, info.TurnID)
	}
	want := []string{"root", "child-1", "grandchild"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree turn IDs=%v, want=%v", got, want)
	}

	if tree := al.GetActiveTurnTree("non-existent-session"); tree != nil {
		t.Fatalf("expected nil tree for unknown session, got %d entries", len(tree))
	}
}

// TestListActiveTurns verifies that the status snapshot includes root turns
// and nested SubTurns, ordered by start time.
func TestListActiveTurns(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "gpt-4o-mini", Provider: "mock"},
		},
	}
	al := NewAgentLoop(cfg, nil, &simpleMockProviderAPI{response: "ok"})

	base := time.Now().Add(-time.Minute)
	al.activeTurnStates.Store("session", &turnState{
		turnID:    "root",
		startedAt: base,
	})
	al.activeTurnStates.Store("child-2", &turnState{
		turnID:       "child-2",
		parentTurnID: "root",
		depth:        1,
		startedAt:    base.Add(2 * time.Second),
	})
	al.activeTurnStates.Store("child-1", &turnState{
		turnID:       "child-1",
		parentTurnID: "root",
		depth:        1,
		startedAt:    base.Add(time.Second),
	})
	defer func() {
		for _, key := range []string{"session", "child-1", "child-2"} {
			al.activeTurnStates.Delete(key)
		}
	}()

	turns := al.ListActiveTurns()
	got := make([]string, 0, len(turns))
	for _, info := range turns {
		got = append(got, info.TurnID)
	}
	want := []string{"root", "child-1", "child-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active turn IDs=%v, want=%v", got, want)
	}

	subagents := make([]string, 0, len(turns))
	for _, info := range turns {
		if info.Depth > 0 {
			subagents = append(subagents, info.TurnID)
		}
	}
	if want := []string{"child-1", "child-2"}; !reflect.DeepEqual(subagents, want) {
		t.Fatalf("active subagents=%v, want=%v", subagents, want)
	}
}

// TestGetActiveTurnTree_AttachesOrphanedAsyncChildren verifies that a subagent
// whose parent turn already finished is listed under the session's current root.
func TestGetActiveTurnTree_AttachesOrphanedAsyncChildren(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "gpt-4o-mini", Provider: "mock"},
		},
	}
	al := NewAgentLoop(cfg, nil, &simpleMockProviderAPI{response: "ok"})

	sessionKey := "orphan-session"
	finished := &turnState{turnID: "finished", sessionKey: sessionKey, childTurnIDs: []string{"async-child"}}
	al.activeTurnStates.Store(sessionKey, &turnState{turnID: "current", sessionKey: sessionKey})
	al.activeTurnStates.Store("async-child", &turnState{
		turnID:          "async-child",
		parentTurnID:    "finished",
		parentTurnState: finished,
		depth:           1,
	})
	al.activeTurnStates.Store("other-child", &turnState{
		turnID:          "other-child",
		parentTurnState: &turnState{turnID: "x", sessionKey: "another-session"},
		depth:           1,
	})
	defer func() {
		for _, key := range []string{sessionKey, "async-child", "other-child"} {
			al.activeTurnStates.Delete(key)
		}
	}()

	tree := al.GetActiveTurnTree(sessionKey)
	if len(tree) != 2 || tree[0].TurnID != "current" || tree[1].TurnID != "async-child" {
		t.Fatalf("tree = %+v, want [current async-child]", tree)
	}
	if tree[1].ParentTurnID != "current" {
		t.Errorf("orphan ParentTurnID = %q, want %q", tree[1].ParentTurnID, "current")
	}
}
