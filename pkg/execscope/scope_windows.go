//go:build windows

package execscope

import (
	"os/exec"
	"strconv"
	"sync"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

type windowsScope struct {
	cfg      Config
	mu       sync.Mutex
	cmd      *exec.Cmd
	started  bool
	killOnce sync.Once
}

func newPlatformScope(cfg Config) (Scope, error) {
	return &windowsScope{cfg: cfg}, nil
}

func (s *windowsScope) Prepare(*exec.Cmd) error {
	return nil
}

func (s *windowsScope) Start(cmd *exec.Cmd) error {
	if err := isolation.StartWithOptions(cmd, isolation.StartOptions{AsPID1: s.cfg.Foreground}); err != nil {
		return err
	}
	s.mu.Lock()
	s.cmd = cmd
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *windowsScope) Kill() error {
	s.killOnce.Do(func() {
		s.mu.Lock()
		cmd := s.cmd
		started := s.started
		s.mu.Unlock()
		if !started || cmd == nil || cmd.Process == nil {
			return
		}
		pid := strconv.Itoa(cmd.Process.Pid)
		_ = exec.Command("taskkill", "/T", "/F", "/PID", pid).Run()
		_ = cmd.Process.Kill()
	})
	return nil
}

func (s *windowsScope) Close() error {
	return nil
}

func (s *windowsScope) MarkExited() {}

func (s *windowsScope) Empty() bool {
	return true
}

func (s *windowsScope) Backend() string {
	return BackendKillPG
}
