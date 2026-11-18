package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

const (
	// execBlockHardStopThreshold is the number of consecutive identical guard
	// rejections after which the model gets a hard-stop message instead of a
	// plain block reason.
	execBlockHardStopThreshold = 3

	// maxExecBlockSessions bounds the process-local breaker state. Each entry
	// holds a hash and a counter, so the table stays in the low KBs.
	maxExecBlockSessions = 64
)

type execBlockKey struct{ agent, session string }

type execBlockState struct {
	fingerprint uint64
	count       int
}

// execBlockBreaker detects a model retrying the same rejected command. State
// is process-local, per agent and conversation, and bounded; no command text
// is retained, only a 64-bit hash and a counter per conversation.
type execBlockBreaker struct {
	mu     sync.Mutex
	states map[execBlockKey]execBlockState
}

func newExecBlockBreaker() *execBlockBreaker {
	return &execBlockBreaker{states: make(map[execBlockKey]execBlockState)}
}

func execBlockSession(ctx context.Context) (execBlockKey, bool) {
	key := execBlockKey{agent: ToolAgentID(ctx), session: ToolSessionKey(ctx)}
	return key, key.agent != "" && key.session != ""
}

// record registers one blocked attempt and reports how many identical blocks
// the conversation has seen in a row. The fingerprint is the canonical command
// only: the same rejected command must keep its streak even when the guard
// explains the rejection differently (for example "path outside working dir"
// followed by "access denied: path is outside the workspace").
func (b *execBlockBreaker) record(ctx context.Context, command string) (int, bool) {
	if b == nil {
		return 0, false
	}
	key, ok := execBlockSession(ctx)
	if !ok {
		return 0, false
	}
	fingerprint := execBlockFingerprint(command)

	b.mu.Lock()
	defer b.mu.Unlock()
	state, tracked := b.states[key]
	if tracked && state.fingerprint == fingerprint {
		state.count++
	} else {
		if !tracked && len(b.states) >= maxExecBlockSessions {
			// Arbitrary eviction keeps the table bounded without an LRU list.
			// A dropped conversation simply starts a new streak.
			for evict := range b.states {
				delete(b.states, evict)
				break
			}
		}
		state = execBlockState{fingerprint: fingerprint, count: 1}
	}
	b.states[key] = state
	return state.count, true
}

// reset clears the streak after an allowed run: the model is making progress.
func (b *execBlockBreaker) reset(ctx context.Context) {
	if b == nil {
		return
	}
	key, ok := execBlockSession(ctx)
	if !ok {
		return
	}
	b.mu.Lock()
	delete(b.states, key)
	b.mu.Unlock()
}

func (b *execBlockBreaker) clear(agentID, sessionKey string) {
	if b == nil || agentID == "" || sessionKey == "" {
		return
	}
	b.mu.Lock()
	delete(b.states, execBlockKey{agent: agentID, session: sessionKey})
	b.mu.Unlock()
}

// execBlockFingerprint hashes the canonical command forms with FNV-1a. A quote
// splice, extra whitespace or a wrapper that resolves to the same argv keeps the
// same streak; a different command starts a new one. A hash collision only
// merges two streaks.
func execBlockFingerprint(command string) uint64 {
	forms := shellguard.CanonicalCommands(strings.TrimSpace(command))
	normalized := strings.Join(forms, "\n")
	if normalized == "" {
		normalized = strings.Join(strings.Fields(command), " ")
	}

	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(normalized); i++ {
		h = (h ^ uint64(normalized[i])) * prime64
	}
	return h
}

func execBlockHardStopMessage(count int) string {
	return fmt.Sprintf(
		"HARD STOP: exec blocked this identical command %d times in a row. "+
			"The guard will keep rejecting it. Do not retry it, reword it, or split it to bypass the guard. "+
			"Tell the user which command is blocked and what input or approval is needed, then continue with a different approach.",
		count,
	)
}

// blockedResult returns the guard reason, escalating to a hard stop when the
// same command and reason have been rejected repeatedly in this conversation.
func (t *ExecTool) blockedResult(ctx context.Context, command, reason string) *ToolResult {
	count, tracked := t.blockBreaker.record(ctx, command)
	if !tracked || count < execBlockHardStopThreshold {
		return ErrorResult(reason)
	}
	return ErrorResult(reason + "\n\n" + execBlockHardStopMessage(count))
}
