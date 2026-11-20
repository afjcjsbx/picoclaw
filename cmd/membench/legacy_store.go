package main

import (
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// LegacyStore is the benchmark baseline: it keeps the full raw conversation in
// memory and returns it verbatim, with no retrieval, ranking, or persistence.
type LegacyStore struct {
	mu      sync.RWMutex
	history map[string][]providers.Message
}

// NewLegacyStore creates a new in-memory conversation store.
func NewLegacyStore() *LegacyStore {
	return &LegacyStore{history: make(map[string][]providers.Message)}
}

// IngestSample loads all turns from a LOCOMO sample into the legacy session store.
func (ls *LegacyStore) IngestSample(sample *LocomoSample) {
	sessionKey := "locomo-" + sample.SampleID
	turns := GetTurns(sample)
	messages := make([]providers.Message, 0, len(turns))
	for _, turn := range turns {
		messages = append(messages, providers.Message{
			Role:    "user",
			Content: turn.Speaker + ": " + turn.Text,
		})
	}

	ls.mu.Lock()
	ls.history[sessionKey] = append(ls.history[sessionKey], messages...)
	ls.mu.Unlock()
}

// GetHistory returns all messages for a sample's session.
func (ls *LegacyStore) GetHistory(sampleID string) []providers.Message {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	history := ls.history["locomo-"+sampleID]
	out := make([]providers.Message, len(history))
	copy(out, history)
	return out
}
