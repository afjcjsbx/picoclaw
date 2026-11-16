// Package execscope owns the lifecycle of an exec child process.
//
// The scope is responsible for making sure that no process spawned by a
// command survives a timeout, an explicit kill, or a normal foreground
// completion. Backends are selected in this order on Linux:
//
//  1. cgroup v2 with clone3(CLONE_INTO_CGROUP) and cgroup.kill
//  2. bubblewrap PID namespace (when isolation is enabled)
//  3. a dedicated subreaper supervisor process
//  4. process-group kill plus descendant enumeration
//
// macOS uses descendant enumeration because it has no subreaper or cgroup
// equivalent. Windows keeps the existing taskkill-based tree kill.
package execscope

import (
	"os/exec"
)

// Backend names reported by Scope.Backend.
const (
	BackendCgroup     = "cgroup"
	BackendPIDNS      = "pidns"
	BackendSupervisor = "supervisor"
	BackendKillPG     = "killpg"
)

// Config describes one execution scope.
type Config struct {
	// Command is the original shell command string, for diagnostics.
	Command string
	// PTY is true when the command runs attached to a pseudo-terminal.
	PTY bool
	// Foreground is true for synchronous runs. Foreground scopes kill any
	// descendant that remains when the initial process exits.
	Foreground bool
}

// Scope is a process containment scope.
type Scope interface {
	// Prepare mutates cmd before it is started (for example to add cgroup
	// fields or to wrap the command in a supervisor).
	Prepare(cmd *exec.Cmd) error
	// Start starts cmd through the shared isolation entry point.
	Start(cmd *exec.Cmd) error
	// MarkExited records that the direct child has been reaped. Backends that
	// only learn about scope emptiness from cmd.Wait use it to report Empty.
	MarkExited()
	// Kill terminates every process in the scope. It is idempotent.
	Kill() error
	// Close releases the scope. Foreground scopes kill leftover members;
	// background scopes normally keep running until Kill or Close.
	Close() error
	// Empty reports whether the scope currently has no live processes.
	Empty() bool
	// Backend returns the selected backend name.
	Backend() string
}

// KillProcessTree is the platform fallback used when no kernel scope is
// available. It kills the process group led by pid and every descendant that
// escaped it with setsid or setpgid.
func KillProcessTree(pid int) error {
	return killProcessTree(pid)
}

// New selects and returns the best scope available on this platform.
func New(cfg Config) (Scope, error) {
	return newPlatformScope(cfg)
}
