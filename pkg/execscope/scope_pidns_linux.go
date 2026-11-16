//go:build linux

package execscope

import (
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

// pidNSScope relies on bubblewrap's --unshare-pid. bwrap is PID 1 in the new
// namespace (or the command itself is PID 1 in foreground mode); killing it
// makes the kernel kill every remaining process in the namespace, including
// children that called setsid.
type pidNSScope struct {
	cfg      Config
	mu       sync.Mutex
	cmd      *exec.Cmd
	started  bool
	exited   atomic.Bool
	killOnce sync.Once
}

func newPIDNSScope(cfg Config) Scope {
	return &pidNSScope{cfg: cfg}
}

func (s *pidNSScope) Prepare(*exec.Cmd) error {
	return nil
}

func (s *pidNSScope) Start(cmd *exec.Cmd) error {
	if err := isolation.StartWithOptions(cmd, isolation.StartOptions{AsPID1: s.cfg.Foreground}); err != nil {
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *pidNSScope) Kill() error {
	s.killOnce.Do(func() {
		s.mu.Lock()
		cmd := s.cmd
		started := s.started
		s.mu.Unlock()
		if !started || cmd == nil || cmd.Process == nil {
			return
		}
		pid := cmd.Process.Pid
		if pid <= 0 {
			return
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
	return nil
}

func (s *pidNSScope) Close() error {
	// bwrap exits only after every process in the namespace is gone (or, in
	// foreground AsPID1 mode, immediately when the command exits). There is
	// nothing to signal after the caller has reaped it.
	return nil
}

func (s *pidNSScope) MarkExited() {
	s.exited.Store(true)
}

func (s *pidNSScope) Empty() bool {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return true
	}
	return s.exited.Load()
}

func (s *pidNSScope) Backend() string {
	return BackendPIDNS
}
