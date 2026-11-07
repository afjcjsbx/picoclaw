package seahorse

import (
	"errors"
	"sync"
)

// ErrStaleCandidates means the context changed between candidate selection and
// the atomic apply of a compaction result. Callers should treat it as a benign
// lost race: the summary was not written and the next pass can retry.
var ErrStaleCandidates = errors.New("seahorse: compaction candidates are stale")

// ErrContextBudgetExceeded means the protected fresh tail cannot be made to fit
// the configured prompt budget.
var ErrContextBudgetExceeded = errors.New("seahorse: context budget exceeded")

// conversationLocks is a keyed mutex manager. Unlike the fixed sharded mutexes
// used by the ingestion hot path, this manager is keyed by conversation ID and
// can therefore be shared by all write paths (ingest, bootstrap, clear, delete,
// compaction). Entries are reference counted and removed when unused, so long
// lived processes do not accumulate one mutex per ever-seen conversation.
type conversationLocks struct {
	mu      sync.Mutex
	entries map[int64]*conversationLock
}

type conversationLock struct {
	mu   sync.Mutex
	refs int
}

func newConversationLocks() *conversationLocks {
	return &conversationLocks{entries: make(map[int64]*conversationLock)}
}

func (l *conversationLocks) acquire(convID int64) *conversationLock {
	l.mu.Lock()
	entry := l.entries[convID]
	if entry == nil {
		entry = &conversationLock{}
		l.entries[convID] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return entry
}

func (l *conversationLocks) release(convID int64, entry *conversationLock) {
	entry.mu.Unlock()

	l.mu.Lock()
	entry.refs--
	if entry.refs <= 0 {
		delete(l.entries, convID)
	}
	l.mu.Unlock()
}

// Lock blocks until the conversation lock is acquired and returns the unlock
// function. It is safe to call on a zero Engine: the lock manager is created on
// first use.
func (e *Engine) lockConversation(convID int64) func() {
	locks := e.conversationLockManager()
	entry := locks.acquire(convID)
	return func() { locks.release(convID, entry) }
}

func (e *Engine) conversationLockManager() *conversationLocks {
	e.locksOnce.Do(func() {
		e.conversationLocks = newConversationLocks()
	})
	return e.conversationLocks
}

// lockConversation blocks until the conversation lock is acquired and returns
// the unlock function. Compaction uses it only around the atomic apply, never
// around the LLM call.
func (e *CompactionEngine) lockConversation(convID int64) func() {
	if e == nil || e.locks == nil {
		return func() {}
	}
	entry := e.locks.acquire(convID)
	return func() { e.locks.release(convID, entry) }
}
