//go:build !windows

package execscope

import (
	"os/exec"
	"sync"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

// killPGScope is the last-resort backend. It enumerates descendants before
// signaling the process group, which covers the reported setsid case while the
// shell is still alive. It cannot follow a double-forked child after the
// intermediate process has exited and been reaped.
type killPGScope struct {
	cfg      Config
	mu       sync.Mutex
	cmd      *exec.Cmd
	started  bool
	killOnce sync.Once
	killErr  error
}

func newKillPGScope(cfg Config) Scope {
	return &killPGScope{cfg: cfg}
}

func (s *killPGScope) Prepare(*exec.Cmd) error {
	return nil
}

func (s *killPGScope) Start(cmd *exec.Cmd) error {
	if err := isolation.StartWithOptions(cmd, isolation.StartOptions{AsPID1: s.cfg.Foreground}); err != nil {
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *killPGScope) Kill() error {
	s.killOnce.Do(func() {
		s.mu.Lock()
		cmd := s.cmd
		started := s.started
		s.mu.Unlock()
		if !started || cmd == nil || cmd.Process == nil {
			return
		}
		s.killErr = killProcessTree(cmd.Process.Pid)
	})
	return s.killErr
}

func (s *killPGScope) MarkExited() {}

func (s *killPGScope) Close() error {
	// Do not signal the saved PID after the caller has reaped the direct child:
	// the PID may have been reused. Kill is used for timeout/explicit kill while
	// the process is still alive.
	return nil
}

func (s *killPGScope) Empty() bool {
	return true
}

func (s *killPGScope) Backend() string {
	return BackendKillPG
}
