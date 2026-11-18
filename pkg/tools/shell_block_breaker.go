package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
// the conversation has seen in a row. A different command or rejection reason
// starts a new streak.
func (b *execBlockBreaker) record(ctx context.Context, command, reason string) (int, bool) {
	if b == nil {
		return 0, false
	}
	key, ok := execBlockSession(ctx)
	if !ok {
		return 0, false
	}
	fingerprint := execBlockFingerprint(strings.TrimSpace(command), reason)

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

// execBlockFingerprint hashes the trimmed command and the guard reason with
// FNV-1a. It allocates nothing, and a hash collision only merges two streaks.
func execBlockFingerprint(command, reason string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(command); i++ {
		h = (h ^ uint64(command[i])) * prime64
	}
	h = (h ^ 0) * prime64
	for i := 0; i < len(reason); i++ {
		h = (h ^ uint64(reason[i])) * prime64
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
	count, tracked := t.blockBreaker.record(ctx, command, reason)
	if !tracked || count < execBlockHardStopThreshold {
		return ErrorResult(reason)
	}
	return ErrorResult(reason + "\n\n" + execBlockHardStopMessage(count))
}

// ClearSession drops breaker state when the owning conversation is cleared.
func (t *ExecTool) ClearSession(agentID, sessionKey string) {
	if t != nil {
		t.blockBreaker.clear(agentID, sessionKey)
	}
}
