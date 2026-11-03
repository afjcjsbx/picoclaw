package tools

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PtyKeyMode represents arrow key encoding mode for PTY sessions.
// Programs send smkx/rmkx sequences to switch between CSI and SS3 modes.
type PtyKeyMode uint8

const (
	PtyKeyModeCSI PtyKeyMode = iota // triggered by rmkx (\x1b[?1l)
	PtyKeyModeSS3                   // triggered by smkx (\x1b[?1h)
)

const PtyKeyModeNotFound PtyKeyMode = 255

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionDone     = errors.New("session already completed")
	ErrPTYNotSupported = errors.New("PTY is not supported on this platform")
	ErrNoStdin         = errors.New("no stdin available")
)

type ProcessSession struct {
	mu           sync.Mutex
	ID           string
	PID          int
	Command      string
	PTY          bool
	Background   bool
	StartTime    int64
	ExitTime     int64
	ExitCode     int
	Status       string
	stdinWriter  io.Writer
	stdoutPipe   io.Reader
	outputBuffer *bytes.Buffer
	outputBase   int64
	totalOutput  int64
	ptyMaster    *os.File

	// ptyKeyMode tracks arrow key encoding mode (CSI vs SS3)
	ptyKeyMode PtyKeyMode

	// done is closed exactly once when the session reaches a terminal state.
	done       chan struct{}
	doneClosed bool
	killed     bool

	// onComplete is installed by ExecuteAsync so a background run can notify
	// the agent when it exits. notifyStarted guards the one-shot watcher.
	onComplete    func()
	notifyStarted bool

	// idleChecks counts consecutive poll/wait calls that saw no new output;
	// lastCheckLen is the total output length they compared against.
	idleChecks   int
	lastCheckLen int64
}

// noteCheck records one poll/wait of a still-running session and returns how
// many consecutive checks in a row saw no new output (the first check on a
// silent session already counts).
func (s *ProcessSession) noteCheck() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n := s.totalOutput; n != s.lastCheckLen {
		s.lastCheckLen, s.idleChecks = n, 0
	} else {
		s.idleChecks++
	}
	return s.idleChecks
}

func (s *ProcessSession) IsDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status == "done" || s.Status == "exited"
}

func (s *ProcessSession) GetPtyKeyMode() PtyKeyMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptyKeyMode
}

func (s *ProcessSession) SetPtyKeyMode(mode PtyKeyMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ptyKeyMode = mode
}

func (s *ProcessSession) GetStatus() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status
}

func (s *ProcessSession) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

func (s *ProcessSession) GetExitCode() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ExitCode
}

func (s *ProcessSession) SetExitCode(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ExitCode = code
}

func (s *ProcessSession) killProcess() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != "running" {
		return ErrSessionDone
	}

	pid := s.PID
	if pid <= 0 {
		return ErrSessionNotFound
	}

	if err := killProcessGroup(pid); err != nil {
		return err
	}

	// Release the session's own descriptors: the reader goroutine can then
	// observe the closure and stop, and a dead session keeps no fd open.
	if s.ptyMaster != nil {
		_ = s.ptyMaster.Close()
	}
	if closer, ok := s.stdinWriter.(io.Closer); ok {
		_ = closer.Close()
	}

	s.killed = true
	s.finishLocked(-1)
	return nil
}

func (s *ProcessSession) Kill() error {
	return s.killProcess()
}

// finish records a terminal state and closes Done exactly once. A killed
// session keeps the -1 exit code set by killProcess; a late reader cannot
// overwrite it with the signal-derived status.
func (s *ProcessSession) finish(exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishLocked(exitCode)
}

func (s *ProcessSession) finishLocked(exitCode int) {
	if s.Status == "done" || s.Status == "exited" {
		return
	}
	s.ExitCode = exitCode
	s.Status = "done"
	if s.ExitTime == 0 {
		s.ExitTime = time.Now().Unix()
	}
	s.ensureDoneLocked()
	if !s.doneClosed {
		close(s.done)
		s.doneClosed = true
	}
}

func (s *ProcessSession) ensureDoneLocked() {
	if s.done == nil {
		s.done = make(chan struct{})
	}
}

// Done returns a channel closed when the session exits or is killed.
func (s *ProcessSession) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureDoneLocked()
	if (s.Status == "done" || s.Status == "exited") && !s.doneClosed {
		close(s.done)
		s.doneClosed = true
	}
	return s.done
}

// SetCompletion installs the one-shot completion callback. The first caller
// wins; ExecuteAsync installs it right after a background session starts.
func (s *ProcessSession) SetCompletion(fn func()) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onComplete == nil {
		s.onComplete = fn
	}
}

// StartNotify arms the completion watcher. It returns false when no callback
// was installed or the watcher was already armed. A killed session does not
// emit a completion notification.
func (s *ProcessSession) StartNotify() bool {
	s.mu.Lock()
	if s.notifyStarted || s.onComplete == nil {
		s.mu.Unlock()
		return false
	}
	s.notifyStarted = true
	fn := s.onComplete
	s.mu.Unlock()

	go func() {
		<-s.Done()
		s.mu.Lock()
		killed := s.killed
		s.mu.Unlock()
		if !killed {
			fn()
		}
	}()
	return true
}

// NotifyEnabled reports whether the completion watcher was armed.
func (s *ProcessSession) NotifyEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notifyStarted
}

// Tail returns the last n bytes retained by the session without consuming
// them. Callers use it for poll/wait/completion previews.
func (s *ProcessSession) Tail(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || s.outputBuffer == nil || s.outputBuffer.Len() == 0 {
		return ""
	}
	data := s.outputBuffer.Bytes()
	if len(data) > n {
		data = data[len(data)-n:]
	}
	return string(data)
}

func (s *ProcessSession) Write(data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != "running" {
		return ErrSessionDone
	}

	var writer io.Writer
	if s.PTY && s.ptyMaster != nil {
		writer = s.ptyMaster
	} else if s.stdinWriter != nil {
		writer = s.stdinWriter
	} else {
		return ErrNoStdin
	}

	_, err := writer.Write([]byte(data))
	return err
}

// appendOutput appends p to the rolling session log. The retained window is
// capped at maxOutputBufferSize; dropped prefixes move outputBase forward so
// absolute offsets remain stable for ReadRange callers.
func (s *ProcessSession) appendOutput(p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.outputBuffer == nil {
		s.outputBuffer = &bytes.Buffer{}
	}
	s.totalOutput += int64(len(p))
	_, _ = s.outputBuffer.Write(p)
	if s.outputBuffer.Len() > maxOutputBufferSize {
		drop := s.outputBuffer.Len() - maxOutputBufferSize
		s.outputBuffer.Next(drop)
		s.outputBase += int64(drop)
	}
}

// Read returns the retained output without consuming it. The idle counter is
// reset so a read counts as observing the current output.
func (s *ProcessSession) Read() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.idleChecks, s.lastCheckLen = 0, s.totalOutput
	if s.outputBuffer == nil || s.outputBuffer.Len() == 0 {
		return ""
	}
	return s.outputBuffer.String()
}

// SessionOutputPage is one page of retained session output. Offsets and byte
// counts are absolute from the start of the session log.
type SessionOutputPage struct {
	Text         string
	Offset       int64
	NextOffset   int64
	TotalBytes   int64
	DroppedBytes int64
	SkippedBytes int64
	HasMore      bool
}

// ReadRange returns at most limit bytes starting at absolute byte offset. It
// never consumes the log. DroppedBytes is the total evicted from the rolling
// window; SkippedBytes is how much of the request was unavailable because the
// offset predates that window.
func (s *ProcessSession) ReadRange(offset, limit int64) SessionOutputPage {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.idleChecks, s.lastCheckLen = 0, s.totalOutput
	if offset < 0 {
		offset = 0
	}
	page := SessionOutputPage{
		Offset:       offset,
		NextOffset:   offset,
		TotalBytes:   s.totalOutput,
		DroppedBytes: s.outputBase,
	}
	if s.outputBuffer == nil || s.outputBuffer.Len() == 0 {
		return page
	}
	if offset < s.outputBase {
		page.SkippedBytes = s.outputBase - offset
		offset = s.outputBase
		page.Offset = offset
		page.NextOffset = offset
	}
	data := s.outputBuffer.Bytes()
	rel := offset - s.outputBase
	if rel >= int64(len(data)) {
		return page
	}
	if limit <= 0 || limit > int64(len(data))-rel {
		limit = int64(len(data)) - rel
	}
	end := rel + limit
	page.Text = string(data[rel:end])
	page.NextOffset = offset + int64(len(page.Text))
	page.HasMore = page.NextOffset < page.TotalBytes
	return page
}

func (s *ProcessSession) TotalBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalOutput
}

func (s *ProcessSession) DroppedBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputBase
}

func (s *ProcessSession) ToSessionInfo() SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	return SessionInfo{
		ID:        s.ID,
		Command:   s.Command,
		Status:    s.Status,
		PID:       s.PID,
		StartedAt: s.StartTime,
		ExitCode:  s.ExitCode,
		ExitedAt:  s.ExitTime,
	}
}

// retentionTime returns the session's terminal state and the timestamp the
// retention window should start from: exit time for completed sessions,
// start time as a fallback for manually constructed or legacy records.
func (s *ProcessSession) retentionTime() (done bool, ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	done = s.Status == "done" || s.Status == "exited"
	ts = s.ExitTime
	if ts == 0 {
		ts = s.StartTime
	}
	return done, ts
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ProcessSession
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*ProcessSession),
		stopCh:   make(chan struct{}),
	}

	// Start cleaner goroutine - runs every 5 minutes, cleans up sessions done for >30 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-sm.stopCh:
				return
			case <-ticker.C:
				sm.cleanupOldSessions()
			}
		}
	}()

	return sm
}

// Stop shuts down the background cleanup goroutine. Safe to call multiple
// times from concurrent goroutines. After Stop returns, the SessionManager
// is still usable — only the cleanup goroutine is terminated.
func (sm *SessionManager) Stop() {
	sm.stopOnce.Do(func() {
		close(sm.stopCh)
	})
}

// cleanupOldSessions removes sessions whose retention window has elapsed.
// The window starts when the session exits, not when it started, so a long
// job keeps its final output readable for the full period.
func (sm *SessionManager) cleanupOldSessions() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-30 * time.Minute)
	for id, session := range sm.sessions {
		done, ts := session.retentionTime()
		if done && ts < cutoff.Unix() {
			delete(sm.sessions, id)
		}
	}
}

func (sm *SessionManager) Add(session *ProcessSession) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[session.ID] = session
}

func (sm *SessionManager) Get(sessionID string) (*ProcessSession, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}

	return session, nil
}

func (sm *SessionManager) Remove(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

func (sm *SessionManager) List() []SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]SessionInfo, 0, len(sm.sessions))
	for _, session := range sm.sessions {
		result = append(result, session.ToSessionInfo())
	}

	return result
}

func generateSessionID() string {
	return uuid.New().String()[:8]
}
