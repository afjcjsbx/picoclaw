//go:build linux

package execscope

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

const supervisorArg = "__exec_supervisor"

func init() {
	if len(os.Args) < 2 || os.Args[1] != supervisorArg {
		return
	}
	os.Exit(runSupervisor(os.Args[2:]))
}

// supervisorScope runs the command under a dedicated subreaper process. The
// subreaper adopts orphaned descendants even when they call setsid or
// double-fork, so they stay discoverable and killable. A control pipe makes
// the supervisor kill its subtree when the parent dies unexpectedly.
type supervisorScope struct {
	cfg        Config
	executable string

	mu           sync.Mutex
	cmd          *exec.Cmd
	controlRead  *os.File
	controlWrite *os.File
	started      bool
	exited       atomic.Bool
	killOnce     sync.Once
}

func newSupervisorScope(cfg Config) (Scope, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if executable == "" {
		return nil, errors.New("cannot resolve current executable for supervisor")
	}
	return &supervisorScope{cfg: cfg, executable: executable}, nil
}

func (s *supervisorScope) Prepare(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return errors.New("invalid exec command")
	}

	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return err
	}

	controlFD := 3 + len(cmd.ExtraFiles)
	args := []string{s.executable, supervisorArg, "--control-fd", strconv.Itoa(controlFD)}
	if s.cfg.Foreground {
		args = append(args, "--foreground")
	}
	if s.cfg.PTY {
		args = append(args, "--pty")
	}
	args = append(args, "--", cmd.Path)
	args = append(args, cmd.Args[1:]...)

	cmd.ExtraFiles = append(cmd.ExtraFiles, controlRead)
	cmd.Path = s.executable
	cmd.Args = args
	// The supervisor itself is not attached to the PTY session. It starts the
	// shell with Setsid when PTY mode is requested.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	s.mu.Lock()
	s.cmd = cmd
	s.controlRead = controlRead
	s.controlWrite = controlWrite
	s.mu.Unlock()
	return nil
}

func (s *supervisorScope) Start(cmd *exec.Cmd) error {
	err := isolation.StartWithOptions(cmd, isolation.StartOptions{})
	s.mu.Lock()
	if s.controlRead != nil {
		_ = s.controlRead.Close()
		s.controlRead = nil
	}
	s.mu.Unlock()
	if err != nil {
		s.closeControl()
		return err
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *supervisorScope) Kill() error {
	s.killOnce.Do(func() {
		s.mu.Lock()
		control := s.controlWrite
		cmd := s.cmd
		s.mu.Unlock()

		if control != nil {
			_, writeErr := control.Write([]byte("k"))
			_ = control.Close()
			if writeErr == nil {
				return
			}
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return nil
}

func (s *supervisorScope) Close() error {
	s.closeControl()
	return nil
}

func (s *supervisorScope) closeControl() {
	s.mu.Lock()
	control := s.controlWrite
	s.controlWrite = nil
	s.mu.Unlock()
	if control != nil {
		// EOF is the supervisor's parent-death signal.
		_ = control.Close()
	}
}

func (s *supervisorScope) MarkExited() {
	s.exited.Store(true)
}

func (s *supervisorScope) Empty() bool {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return true
	}
	return s.exited.Load()
}

func (s *supervisorScope) Backend() string {
	return BackendSupervisor
}

func runSupervisor(args []string) int {
	foreground := false
	ptyMode := false
	controlFD := 3
	index := 0
	for index < len(args) {
		switch args[index] {
		case "--foreground":
			foreground = true
			index++
		case "--pty":
			ptyMode = true
			index++
		case "--control-fd":
			if index+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "exec supervisor: missing control fd")
				return 127
			}
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 3 {
				fmt.Fprintln(os.Stderr, "exec supervisor: invalid control fd")
				return 127
			}
			controlFD = value
			index += 2
		case "--":
			index++
			goto command
		default:
			fmt.Fprintf(os.Stderr, "exec supervisor: unknown argument %q\n", args[index])
			return 127
		}
	}

command:
	if index >= len(args) {
		fmt.Fprintln(os.Stderr, "exec supervisor: missing command")
		return 127
	}
	path := args[index]
	commandArgs := append([]string{path}, args[index+1:]...)

	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "exec supervisor: set child subreaper: %v\n", err)
		return 127
	}

	sysAttr := &syscall.SysProcAttr{Setpgid: true}
	if ptyMode {
		// The shell, not the supervisor, becomes the PTY session leader.
		sysAttr = &syscall.SysProcAttr{Setsid: true}
	}
	proc, err := os.StartProcess(path, commandArgs, &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   sysAttr,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "exec supervisor: start %s: %v\n", path, err)
		return 127
	}

	var killOnce sync.Once
	var externalKill atomic.Bool
	killTree := func() {
		killOnce.Do(func() { killSupervisedTree(proc.Pid) })
	}
	killExternal := func() {
		externalKill.Store(true)
		killTree()
	}

	if control := os.NewFile(uintptr(controlFD), "exec-scope-control"); control != nil {
		go func() {
			defer control.Close()
			var buf [1]byte
			for {
				n, readErr := control.Read(buf[:])
				if n > 0 {
					killExternal()
					return
				}
				if readErr != nil {
					if errors.Is(readErr, io.EOF) {
						killExternal()
					}
					return
				}
			}
		}()
	}

	var shellStatus syscall.WaitStatus
	shellSeen := false
	for {
		var status syscall.WaitStatus
		pid, waitErr := syscall.Wait4(-1, &status, 0, nil)
		if waitErr == syscall.EINTR {
			continue
		}
		if waitErr == syscall.ECHILD {
			break
		}
		if waitErr != nil {
			break
		}
		if pid == proc.Pid {
			shellStatus = status
			shellSeen = true
			if foreground {
				// Do not block the reaper: the main wait4 loop must keep
				// reaping children while killSupervisedTree enumerates them.
				go killTree()
			}
		}
	}

	if externalKill.Load() {
		return 128 + int(syscall.SIGKILL)
	}
	if !shellSeen {
		return 1
	}
	return supervisorExitCode(shellStatus)
}

func supervisorExitCode(status syscall.WaitStatus) int {
	if status.Exited() {
		return status.ExitStatus()
	}
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}

func killSupervisedTree(shellPID int) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = syscall.Kill(-shellPID, syscall.SIGKILL)
		_ = syscall.Kill(shellPID, syscall.SIGKILL)

		children := supervisedChildPIDs(os.Getpid())
		if len(children) == 0 {
			return
		}
		for _, pid := range children {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func supervisedChildPIDs(pid int) []int {
	taskDir := "/proc/" + strconv.Itoa(pid) + "/task"
	tasks, err := os.ReadDir(taskDir)
	if err == nil {
		seen := make(map[int]struct{})
		for _, task := range tasks {
			data, readErr := os.ReadFile(taskDir + "/" + task.Name() + "/children")
			if readErr != nil {
				continue
			}
			for _, field := range strings.Fields(string(data)) {
				child, parseErr := strconv.Atoi(field)
				if parseErr == nil && child > 0 && child != pid {
					seen[child] = struct{}{}
				}
			}
		}
		children := make([]int, 0, len(seen))
		for child := range seen {
			children = append(children, child)
		}
		return children
	}

	// Fallback for kernels without /proc/<pid>/task/<tid>/children.
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var children []int
	for _, entry := range entries {
		child, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || child <= 0 {
			continue
		}
		data, readErr := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if readErr != nil {
			continue
		}
		text := string(data)
		closeParen := strings.LastIndexByte(text, ')')
		if closeParen < 0 || closeParen+1 >= len(text) {
			continue
		}
		fields := strings.Fields(text[closeParen+1:])
		if len(fields) < 2 {
			continue
		}
		ppid, parseErr := strconv.Atoi(fields[1])
		if parseErr == nil && ppid == pid {
			children = append(children, child)
		}
	}
	return children
}
