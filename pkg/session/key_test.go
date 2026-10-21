package session

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/routing"
)

type testScopeReader struct {
	scope *SessionScope
}

func (r testScopeReader) GetSessionScope(sessionKey string) *SessionScope {
	return CloneScope(r.scope)
}

func TestIsExplicitSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"sk_v1_abc", true},
		{"agent:main:direct:user123", true},
		{"custom-key", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsExplicitSessionKey(tt.key); got != tt.want {
			t.Fatalf("IsExplicitSessionKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestParseLegacyAgentSessionKey(t *testing.T) {
	parsed := ParseLegacyAgentSessionKey("agent:sales:telegram:direct:user123")
	if parsed == nil {
		t.Fatal("expected parsed legacy key, got nil")
	}
	if parsed.AgentID != "sales" {
		t.Fatalf("AgentID = %q, want sales", parsed.AgentID)
	}
	if parsed.Rest != "telegram:direct:user123" {
		t.Fatalf("Rest = %q, want telegram:direct:user123", parsed.Rest)
	}

	if got := ParseLegacyAgentSessionKey("sk_v1_abc"); got != nil {
		t.Fatalf("expected nil for opaque key, got %+v", got)
	}
}

func TestBuildLegacyDirectAliases(t *testing.T) {
	aliases := BuildLegacyDirectAliases("Main", "Telegram", "BotA", "User123")
	want := []string{
		"agent:main:direct:user123",
		"agent:main:telegram:direct:user123",
		"agent:main:telegram:bota:direct:user123",
	}
	if len(aliases) != len(want) {
		t.Fatalf("len(aliases) = %d, want %d", len(aliases), len(want))
	}
	for i := range want {
		if aliases[i] != want[i] {
			t.Fatalf("aliases[%d] = %q, want %q", i, aliases[i], want[i])
		}
	}
}

func TestBuildLegacyPeerAlias(t *testing.T) {
	got := BuildLegacyPeerAlias("Main", "Slack", "channel", "C001")
	if got != "agent:main:slack:channel:c001" {
		t.Fatalf("BuildLegacyPeerAlias() = %q", got)
	}
}

func TestBuildMainSessionKey(t *testing.T) {
	got := BuildMainSessionKey("Main")
	if !IsOpaqueSessionKey(got) {
		t.Fatalf("BuildMainSessionKey() = %q, want opaque key", got)
	}
	if got != BuildOpaqueSessionKey("agent:main:main") {
		t.Fatalf("BuildMainSessionKey() = %q, want stable main-key hash", got)
	}
}

func TestIsMainSessionAlias(t *testing.T) {
	tests := []struct {
		name    string
		agentID string
		alias   string
		want    bool
	}{
		{"legacy main for sales", "sales", "agent:sales:main", true},
		{"legacy main for default agent", "main", "agent:main:main", true},
		{"legacy main of another agent", "sales", "agent:other:main", true},
		{"legacy main with case variations", "sales", "agent:Sales:MAIN", true},
		{"legacy main with empty agent ID", "", "agent:main:main", true},
		{"opaque main for sales", "sales", BuildMainSessionKey("sales"), true},
		{"opaque main for default agent", "main", BuildMainSessionKey("main"), true},
		{"opaque default main for another agent", "sales", BuildMainSessionKey(routing.DefaultAgentID), true},
		{"empty alias", "sales", "", false},
		{"direct main is not a main alias", "sales", "agent:sales:direct:main", false},
		{"legacy direct alias", "sales", "agent:sales:direct:x", false},
		{"opaque direct key", "sales", BuildOpaqueSessionKey("agent:sales:direct:x"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMainSessionAlias(tt.agentID, tt.alias); got != tt.want {
				t.Fatalf("isMainSessionAlias(%q, %q) = %v, want %v", tt.agentID, tt.alias, got, tt.want)
			}
		})
	}
}

func TestResolveAgentID_PrefersSessionScope(t *testing.T) {
	store := testScopeReader{
		scope: &SessionScope{
			Version: ScopeVersionV1,
			AgentID: "Support",
			Channel: "slack",
		},
	}

	if got := ResolveAgentID(store, "sk_v1_anything"); got != "support" {
		t.Fatalf("ResolveAgentID() = %q, want support", got)
	}
}

func TestResolveAgentID_FallsBackToLegacyKey(t *testing.T) {
	if got := ResolveAgentID(nil, "agent:Sales:telegram:direct:user123"); got != "sales" {
		t.Fatalf("ResolveAgentID() = %q, want sales", got)
	}
}
