package loop

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
)

type loopStatus uint8

const (
	loopStatusNone loopStatus = iota
	loopStatusWarning
	loopStatusCritical
)

// hashToolCall hashes the exact semantic tool call. encoding/json emits map
// keys in a stable order, so equivalent argument maps hash identically without
// discarding meaningful values such as page numbers, paths, timestamps, or IDs.
func hashToolCall(toolName string, args map[string]any) uint64 {
	payload, err := json.Marshal(struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{Name: toolName, Arguments: args})
	if err != nil {
		// Tool arguments originate from JSON. This fallback only protects custom
		// in-process tools that supply a value unsupported by encoding/json.
		payload = []byte(toolName)
	}
	sum := sha256.Sum256(payload)
	return binary.BigEndian.Uint64(sum[:8])
}

// recordToolCall adds a call to the bounded history and reports a transition.
// Warning is emitted once, when the repeat threshold is first reached.
func (ts *turnState) recordToolCall(toolName string, args map[string]any) (loopStatus, int) {
	if ts == nil || !ts.loopDetectionConfig.Enabled {
		return loopStatusNone, 0
	}
	if toolName == "spawn_status" {
		// Polling the same task can return a new status without changing arguments.
		ts.loopDetectionHistory = ts.loopDetectionHistory[:0]
		ts.loopDetectionNext = 0
		return loopStatusNone, 0
	}

	cfg := ts.loopDetectionConfig.Normalized()
	h := hashToolCall(toolName, args)
	if len(ts.loopDetectionHistory) < cfg.WindowSize {
		ts.loopDetectionHistory = append(ts.loopDetectionHistory, h)
		if len(ts.loopDetectionHistory) == cfg.WindowSize {
			ts.loopDetectionNext = 0
		}
	} else {
		ts.loopDetectionHistory[ts.loopDetectionNext] = h
		ts.loopDetectionNext = (ts.loopDetectionNext + 1) % cfg.WindowSize
	}

	count := 0
	latest := len(ts.loopDetectionHistory) - 1
	if len(ts.loopDetectionHistory) == cfg.WindowSize {
		latest = (ts.loopDetectionNext - 1 + cfg.WindowSize) % cfg.WindowSize
	}
	for offset := 0; offset < len(ts.loopDetectionHistory); offset++ {
		i := latest - offset
		if i < 0 {
			i += cfg.WindowSize
		}
		if ts.loopDetectionHistory[i] != h {
			break
		}
		count++
	}

	switch {
	case count >= cfg.CriticalThreshold:
		return loopStatusCritical, count
	case count == cfg.RepeatThreshold:
		return loopStatusWarning, count
	default:
		return loopStatusNone, count
	}
}
