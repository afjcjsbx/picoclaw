package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/routing"
)

const (
	sessionKeyV1Prefix          = "sk_v1_"
	legacyAgentSessionKeyPrefix = "agent:"
)

type parsedLegacySessionKey struct {
	AgentID string
	Rest    string
}

// BuildOpaqueSessionKey returns a stable opaque session key derived from a
// canonical alias string. The alias remains available through metadata for
// compatibility and migration purposes.
func BuildOpaqueSessionKey(alias string) string {
	normalized := strings.TrimSpace(strings.ToLower(alias))
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return sessionKeyV1Prefix + hex.EncodeToString(sum[:])
}

// IsOpaqueSessionKey returns true when the key matches the current opaque
// session-key format.
func IsOpaqueSessionKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), sessionKeyV1Prefix)
}

func isLegacyAgentSessionKey(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), legacyAgentSessionKeyPrefix)
}

func IsExplicitSessionKey(key string) bool {
	return IsOpaqueSessionKey(key) || isLegacyAgentSessionKey(key)
}

func parseLegacySessionKey(sessionKey string) *parsedLegacySessionKey {
	raw := strings.TrimSpace(sessionKey)
	if raw == "" {
		return nil
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 3 || parts[0] != "agent" {
		return nil
	}
	agentID := strings.TrimSpace(parts[1])
	rest := parts[2]
	if agentID == "" || rest == "" {
		return nil
	}
	return &parsedLegacySessionKey{AgentID: agentID, Rest: rest}
}

// ScopeReader is implemented by session stores that expose structured scope
// metadata for a session key. It is the consumer-side contract used by
// ResolveAgentID.
type ScopeReader interface {
	GetSessionScope(sessionKey string) *SessionScope
}

// ResolveAgentID returns the routed agent ID associated with a session. It
// prefers structured session scope metadata when the reader provides it and
// falls back to legacy agent-scoped session keys for compatibility.
func ResolveAgentID(reader ScopeReader, sessionKey string) string {
	if reader != nil {
		if scope := reader.GetSessionScope(sessionKey); scope != nil && strings.TrimSpace(scope.AgentID) != "" {
			return routing.NormalizeAgentID(scope.AgentID)
		}
	}

	if parsed := parseLegacySessionKey(sessionKey); parsed != nil {
		return routing.NormalizeAgentID(parsed.AgentID)
	}

	return ""
}

func buildLegacyMainAlias(agentID string) string {
	return fmt.Sprintf("agent:%s:main", routing.NormalizeAgentID(agentID))
}

// BuildMainSessionKey returns the canonical opaque main-session key for an
// agent. The corresponding legacy alias remains available via
// buildLegacyMainAlias for compatibility and migration logic.
func BuildMainSessionKey(agentID string) string {
	return BuildOpaqueSessionKey(buildLegacyMainAlias(agentID))
}

// isMainSessionAlias reports whether alias is the legacy or opaque main-session
// key of agentID. The main session is a shared fallback: its history must not
// be promoted into individual per-channel sessions.
func isMainSessionAlias(agentID, alias string) bool {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return false
	}
	if strings.EqualFold(alias, buildLegacyMainAlias(agentID)) {
		return true
	}
	if strings.EqualFold(alias, BuildMainSessionKey(agentID)) {
		return true
	}
	// Any legacy main alias ("agent:<x>:main") counts, regardless of which
	// agent it belongs to.
	if parsed := parseLegacySessionKey(alias); parsed != nil &&
		strings.EqualFold(strings.TrimSpace(parsed.Rest), "main") {
		return true
	}
	return strings.EqualFold(alias, BuildMainSessionKey(routing.DefaultAgentID))
}

func buildLegacyDirectAliases(agentID, channel, account, peerID string) []string {
	agentID = routing.NormalizeAgentID(agentID)
	channel = normalizeLegacyChannel(channel)
	account = routing.NormalizeAccountID(account)
	peerID = strings.ToLower(strings.TrimSpace(peerID))
	if peerID == "" {
		return nil
	}
	return []string{
		fmt.Sprintf("agent:%s:direct:%s", agentID, peerID),
		fmt.Sprintf("agent:%s:%s:direct:%s", agentID, channel, peerID),
		fmt.Sprintf("agent:%s:%s:%s:direct:%s", agentID, channel, account, peerID),
	}
}

func buildLegacyPeerAlias(agentID, channel, peerKind, peerID string) string {
	agentID = routing.NormalizeAgentID(agentID)
	channel = normalizeLegacyChannel(channel)
	peerKind = strings.ToLower(strings.TrimSpace(peerKind))
	if peerKind == "" {
		peerKind = "unknown"
	}
	peerID = strings.ToLower(strings.TrimSpace(peerID))
	if peerID == "" {
		peerID = "unknown"
	}
	return fmt.Sprintf("agent:%s:%s:%s:%s", agentID, channel, peerKind, peerID)
}

// canonicalSessionIdentityID collapses an identity using identity_links when
// possible, then returns a normalized lowercase identifier.
func canonicalSessionIdentityID(channel, rawID string, identityLinks map[string][]string) string {
	normalizedID := strings.TrimSpace(rawID)
	if normalizedID == "" {
		return ""
	}
	if linked := resolveLinkedPeerID(identityLinks, channel, normalizedID); linked != "" {
		normalizedID = linked
	}
	return strings.ToLower(normalizedID)
}

func normalizeLegacyChannel(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		return "unknown"
	}
	return channel
}

func resolveLinkedPeerID(identityLinks map[string][]string, channel, peerID string) string {
	if len(identityLinks) == 0 {
		return ""
	}
	peerID = strings.TrimSpace(peerID)
	if peerID == "" {
		return ""
	}

	candidates := make(map[string]bool)
	rawCandidate := strings.ToLower(peerID)
	if rawCandidate != "" {
		candidates[rawCandidate] = true
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "" {
		candidates[fmt.Sprintf("%s:%s", channel, rawCandidate)] = true
	}
	if idx := strings.Index(rawCandidate, ":"); idx > 0 && idx < len(rawCandidate)-1 {
		candidates[rawCandidate[idx+1:]] = true
	}

	for canonical, ids := range identityLinks {
		canonicalName := strings.TrimSpace(canonical)
		if canonicalName == "" {
			continue
		}
		for _, id := range ids {
			normalized := strings.ToLower(strings.TrimSpace(id))
			if normalized != "" && candidates[normalized] {
				return canonicalName
			}
		}
	}
	return ""
}

// canonicalScopeSignature returns a stable serialized representation of scope.
func canonicalScopeSignature(scope SessionScope) string {
	parts := []string{
		fmt.Sprintf("v=%d", scope.Version),
		fmt.Sprintf("agent=%s", strings.TrimSpace(strings.ToLower(scope.AgentID))),
		fmt.Sprintf("channel=%s", strings.TrimSpace(strings.ToLower(scope.Channel))),
		fmt.Sprintf("account=%s", strings.TrimSpace(strings.ToLower(scope.Account))),
	}
	for _, dimension := range scope.Dimensions {
		dimension = strings.TrimSpace(strings.ToLower(dimension))
		if dimension == "" {
			continue
		}
		value := strings.TrimSpace(strings.ToLower(scope.Values[dimension]))
		parts = append(parts, fmt.Sprintf("%s=%s", dimension, value))
	}
	return strings.Join(parts, "|")
}

// buildSessionKey returns the current opaque key for a structured session scope.
func buildSessionKey(scope SessionScope) string {
	return BuildOpaqueSessionKey(canonicalScopeSignature(scope))
}
