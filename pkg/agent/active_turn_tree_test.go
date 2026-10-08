package agent

import (
	"reflect"
	"testing"

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
