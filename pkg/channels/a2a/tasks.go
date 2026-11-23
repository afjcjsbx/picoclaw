package a2a

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
)

const (
	// maxAgentText bounds the reply kept per task. Streamed deltas past the cap
	// still reach live subscribers; only the stored copy is cut.
	maxAgentText = 64 << 10
	maxUserText  = 2 << 10
	// taskTTL is how long a finished task stays queryable. Expiry is lazy
	// (swept on submit/list), so an idle gateway never wakes up for it.
	taskTTL = time.Hour
	// subBuffer is the per-subscriber frame backlog; a slower client is dropped
	// and can re-attach with SubscribeToTask (it gets a full snapshot).
	subBuffer = 32
)

var (
	errFull    = errors.New("task registry full")
	errBusy    = errors.New("too many running tasks")
	errCtxBusy = errors.New("a task is already running in this context")
	errMsgDup  = errors.New("messageId was already used in a different context")
)

type task struct {
	id, contextID, principal, chatID, messageID string

	userText, agentText string
	reason              string // status message: failure cause or tool progress
	state               string
	updated, created    time.Time

	streaming bool // a client is attached to receive deltas
	chunks    int  // artifact chunks emitted so far

	done  chan struct{}
	subs  map[chan []byte]struct{}
	timer *time.Timer
}

// registry keeps tasks in RAM. All state changes go through it under one
// mutex, so event order seen by subscribers equals transition order.
type registry struct {
	mu     sync.Mutex
	tasks  map[string]*task
	byMsg  map[string]*task // principal\x00messageId -> task (idempotency)
	byChat map[string]*task // running task per chat (= per principal+context)
	max    int
}

func newRegistry(limit int) *registry {
	return &registry{
		tasks:  make(map[string]*task),
		byMsg:  make(map[string]*task),
		byChat: make(map[string]*task),
		max:    limit,
	}
}

func chatKey(principal, contextID string) string { return "a2a:" + principal + ":" + contextID }

// submit registers a task, or returns the existing one for a retried messageId.
// An empty contextID means "any": a retry without one matches the original
// task, and a new task gets a generated one. A retry naming another context
// is rejected rather than answered with a task from the wrong conversation.
func (r *registry) submit(
	principal, messageID, contextID, text string,
	streaming bool,
	maxActive int,
) (*task, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.sweep(now)
	if t := r.byMsg[principal+"\x00"+messageID]; t != nil {
		if contextID != "" && contextID != t.contextID {
			return nil, false, errMsgDup
		}
		return t, true, nil
	}
	if contextID == "" {
		contextID = "ctx-" + proto.NewID()
	}
	chat := chatKey(principal, contextID)
	switch {
	case r.byChat[chat] != nil:
		return nil, false, errCtxBusy
	case len(r.byChat) >= maxActive:
		return nil, false, errBusy
	case len(r.tasks) >= r.max && !r.evictOldestTerminal():
		return nil, false, errFull
	}
	t := &task{
		id: proto.NewID(), contextID: contextID, principal: principal, chatID: chat, messageID: messageID,
		userText: clip(text, maxUserText), state: proto.StateSubmitted, created: now, updated: now,
		streaming: streaming, done: make(chan struct{}), subs: map[chan []byte]struct{}{},
	}
	r.tasks[t.id] = t
	r.byMsg[principal+"\x00"+messageID] = t
	r.byChat[chat] = t
	return t, false, nil
}

// sweep drops finished tasks older than taskTTL. r.mu held.
func (r *registry) sweep(now time.Time) {
	for _, t := range r.tasks {
		if proto.IsTerminal(t.state) && now.Sub(t.updated) > taskTTL {
			r.drop(t)
		}
	}
}

func (r *registry) evictOldestTerminal() bool {
	var oldest *task
	for _, t := range r.tasks {
		if proto.IsTerminal(t.state) && (oldest == nil || t.updated.Before(oldest.updated)) {
			oldest = t
		}
	}
	if oldest == nil {
		return false
	}
	r.drop(oldest)
	return true
}

func (r *registry) drop(t *task) {
	delete(r.tasks, t.id)
	delete(r.byMsg, t.principal+"\x00"+t.messageID)
}

// arm sets the task deadline.
func (r *registry) arm(t *task, d time.Duration, f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !proto.IsTerminal(t.state) {
		t.timer = time.AfterFunc(d, f)
	}
}

// transition moves t to state; false when it was already terminal.
func (r *registry) transition(t *task, state, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.move(t, state, reason)
}

func (r *registry) move(t *task, state, reason string) bool {
	if proto.IsTerminal(t.state) {
		return false
	}
	t.state, t.reason, t.updated = state, reason, time.Now()
	r.emit(t, proto.StreamResult{StatusUpdate: &proto.StatusUpdate{
		TaskID: t.id, ContextID: t.contextID, Status: t.status(),
	}})
	if !proto.IsTerminal(state) {
		return true
	}
	if r.byChat[t.chatID] == t {
		delete(r.byChat, t.chatID)
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	close(t.done)
	for ch := range t.subs {
		close(ch)
	}
	t.subs = nil
	return true
}

// appendText adds a reply fragment, streaming it to subscribers.
func (r *registry) appendText(t *task, delta string, newSegment bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if proto.IsTerminal(t.state) {
		return
	}
	if newSegment && t.agentText != "" {
		delta = "\n\n" + delta
	}
	r.addText(t, delta, false)
}

// addText stores and emits a chunk. r.mu held.
func (r *registry) addText(t *task, delta string, last bool) {
	if room := maxAgentText - len(t.agentText); room > 0 {
		t.agentText += clip(delta, room)
	}
	r.emit(t, proto.StreamResult{ArtifactUpdate: &proto.ArtifactUpdate{
		TaskID: t.id, ContextID: t.contextID,
		Artifact: proto.Artifact{ArtifactID: "artifact-" + t.id, Parts: []proto.Part{proto.TextPart(delta)}},
		Append:   t.chunks > 0, LastChunk: last,
	}})
	t.chunks++
}

// finish completes t with the final reply. Text already streamed is kept;
// otherwise the reply goes out as a single chunk.
func (r *registry) finish(t *task, final string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if proto.IsTerminal(t.state) {
		return
	}
	if t.chunks == 0 {
		r.addText(t, final, true)
	} else {
		r.addText(t, "", true)
	}
	r.move(t, proto.StateCompleted, "")
}

// progress publishes a transient "working" note to attached clients only.
func (r *registry) progress(t *task, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(t.subs) == 0 || proto.IsTerminal(t.state) {
		return
	}
	t.reason = clip(note, 200)
	r.emit(t, proto.StreamResult{StatusUpdate: &proto.StatusUpdate{
		TaskID: t.id, ContextID: t.contextID, Status: t.status(),
	}})
}

// emit fans one frame out; a full subscriber is dropped, never waited on.
func (r *registry) emit(t *task, res proto.StreamResult) {
	if len(t.subs) == 0 {
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		return
	}
	for ch := range t.subs {
		select {
		case ch <- b:
		default:
			close(ch)
			delete(t.subs, ch)
		}
	}
}

// markStreaming flags that a client wants deltas (takes effect on the next LLM call).
func (r *registry) markStreaming(t *task) {
	r.mu.Lock()
	t.streaming = true
	r.mu.Unlock()
}

func (r *registry) isStreaming(t *task) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return t.streaming
}

// subscribe returns a snapshot and a live frame channel (nil if terminal).
func (r *registry) subscribe(t *task) (proto.Task, chan []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := t.snap(nil, true)
	if proto.IsTerminal(t.state) {
		return snap, nil
	}
	ch := make(chan []byte, subBuffer)
	t.subs[ch] = struct{}{}
	return snap, ch
}

func (r *registry) unsubscribe(t *task, ch chan []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(t.subs, ch) // closing is the emitter's job
}

// get returns a task only to its owner.
func (r *registry) get(principal, id string) *task {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.tasks[id]; t != nil && t.principal == principal {
		return t
	}
	return nil
}

// snapshot copies a task for serialization outside the lock.
func (r *registry) snapshot(t *task, hist *int, artifacts bool) proto.Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	return t.snap(hist, artifacts)
}

// closeAll fails every running task (channel shutdown).
func (r *registry) closeAll() []*task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stopped []*task
	for _, t := range r.tasks {
		if r.move(t, proto.StateFailed, "server stopping") {
			stopped = append(stopped, t)
		}
	}
	return stopped
}

// list returns one page of the principal's tasks, newest first, plus the match count.
func (r *registry) list(
	principal, contextID, state string,
	size, offset int,
	hist *int,
	artifacts bool,
) ([]proto.Task, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep(time.Now())
	var match []*task
	for _, t := range r.tasks {
		if t.principal == principal && (contextID == "" || t.contextID == contextID) &&
			(state == "" || t.state == state) {
			match = append(match, t)
		}
	}
	sort.Slice(match, func(i, j int) bool { return match[i].updated.After(match[j].updated) })
	total := len(match)
	if offset > total {
		offset = total
	}
	match = match[offset:min(offset+size, total)]
	out := make([]proto.Task, len(match))
	for i, t := range match {
		out[i] = t.snap(hist, artifacts)
	}
	return out, total
}

func (t *task) status() proto.TaskStatus {
	st := proto.TaskStatus{State: t.state, Timestamp: t.updated.UTC().Format(time.RFC3339)}
	if t.reason != "" {
		st.Message = &proto.Message{
			MessageID: t.id + "-status", ContextID: t.contextID, TaskID: t.id, Role: proto.RoleAgent,
			Parts: []proto.Part{proto.TextPart(t.reason)},
		}
	}
	return st
}

// snap builds the wire Task. History is synthetic: the user message plus the
// reply once finished; hist nil means all of it, n keeps the last n.
func (t *task) snap(hist *int, artifacts bool) proto.Task {
	out := proto.Task{ID: t.id, ContextID: t.contextID, Status: t.status()}
	if artifacts && t.agentText != "" {
		out.Artifacts = []proto.Artifact{
			{ArtifactID: "artifact-" + t.id, Parts: []proto.Part{proto.TextPart(t.agentText)}},
		}
	}
	if hist != nil && *hist <= 0 {
		return out
	}
	out.History = []proto.Message{{
		MessageID: t.messageID, ContextID: t.contextID, TaskID: t.id, Role: proto.RoleUser,
		Parts: []proto.Part{proto.TextPart(t.userText)},
	}}
	if proto.IsTerminal(t.state) && t.agentText != "" {
		out.History = append(out.History, proto.Message{
			MessageID: t.id + "-reply", ContextID: t.contextID, TaskID: t.id, Role: proto.RoleAgent,
			Parts: []proto.Part{proto.TextPart(t.agentText)},
		})
	}
	if hist != nil && *hist < len(out.History) {
		out.History = out.History[len(out.History)-*hist:]
	}
	return out
}

// clip cuts s to at most n bytes without splitting a rune.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func pageToken(offset int) string { return strconv.Itoa(offset) }
