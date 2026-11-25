package tools

import (
	"context"
	"fmt"
	"strings"
)

// execBlockHardStopThreshold is the number of consecutive identical guard
// rejections after which the model gets a hard-stop message instead of a
// plain block reason.
const execBlockHardStopThreshold = 3

// blockStreak is the run of identical rejections in one conversation. No
// command text is retained, only a 64-bit hash and a counter.
type blockStreak struct {
	fingerprint uint64
	count       int
}

// recordBlock registers one blocked attempt and reports how many identical
// blocks the conversation has seen in a row. A different command or rejection
// reason starts a new streak.
func (s *execStateStore) recordBlock(ctx context.Context, command, reason string) (int, bool) {
	fingerprint := execBlockFingerprint(strings.TrimSpace(command), reason)
	count := 0
	tracked := s.update(ctx, true, func(entry *execStateEntry) {
		if entry.block.count > 0 && entry.block.fingerprint == fingerprint {
			entry.block.count++
		} else {
			entry.block = blockStreak{fingerprint: fingerprint, count: 1}
		}
		count = entry.block.count
	})
	return count, tracked
}

// resetBlock clears the streak after an allowed run: the model is making
// progress.
func (s *execStateStore) resetBlock(ctx context.Context) {
	s.update(ctx, false, func(entry *execStateEntry) { entry.block = blockStreak{} })
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
	count, tracked := t.state.recordBlock(ctx, command, reason)
	if !tracked || count < execBlockHardStopThreshold {
		return ErrorResult(reason)
	}
	return ErrorResult(reason + "\n\n" + execBlockHardStopMessage(count))
}
