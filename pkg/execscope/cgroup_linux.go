//go:build linux

package execscope

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

var cgroupDetect struct {
	once   sync.Once
	root   string
	kill   bool
	freeze bool
	err    error
}

var cgroupInstance struct {
	once sync.Once
	path string
	err  error
}

var cgroupScopeSeq atomic.Uint64

// newCgroupScope returns an error when cgroup v2 is not writable, allowing the
// caller to fall back to another backend.
func newCgroupScope(cfg Config) (Scope, error) {
	root, killAvailable, freezeAvailable, err := detectCgroup()
	if err != nil {
		return nil, err
	}
	parent, err := instanceCgroupPath(root)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("exec-%d-%d-%s", os.Getpid(), cgroupScopeSeq.Add(1), randomToken())
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, err
	}
	fd, err := unix.Open(dir, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	return &cgroupScope{
		cfg:           cfg,
		dir:           dir,
		fd:            fd,
		killAvailable: killAvailable,
		freeze:        freezeAvailable,
	}, nil
}

func detectCgroup() (root string, killAvailable, freezeAvailable bool, err error) {
	cgroupDetect.once.Do(func() {
		cgroupDetect.root, cgroupDetect.kill, cgroupDetect.freeze, cgroupDetect.err = probeCgroup()
	})
	return cgroupDetect.root, cgroupDetect.kill, cgroupDetect.freeze, cgroupDetect.err
}

func probeCgroup() (string, bool, bool, error) {
	mountPoint, err := cgroup2MountPoint()
	if err != nil {
		return "", false, false, err
	}
	current, err := currentCgroupPath()
	if err != nil {
		return "", false, false, err
	}
	root := filepath.Clean(filepath.Join(mountPoint, current))
	if _, err := os.Stat(root); err != nil {
		return "", false, false, err
	}

	probe := filepath.Join(root, fmt.Sprintf(".picoclaw-probe-%d-%s", os.Getpid(), randomToken()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		return "", false, false, err
	}
	defer os.Remove(probe)

	// Verify clone3(CLONE_INTO_CGROUP) support before selecting this backend.
	// Some seccomp profiles allow cgroupfs writes but reject clone3; without
	// this probe the first user command would fail to start.
	if err := probeCgroupClone(probe); err != nil {
		return "", false, false, err
	}

	_, killErr := os.Stat(filepath.Join(root, "cgroup.kill"))
	_, freezeErr := os.Stat(filepath.Join(root, "cgroup.freeze"))
	return root, killErr == nil, freezeErr == nil, nil
}

func probeCgroupClone(dir string) error {
	fd, err := unix.Open(dir, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    fd,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Wait()
	return nil
}

func cgroup2MountPoint() (string, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 {
			continue
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || separator+1 >= len(fields) || fields[separator+1] != "cgroup2" {
			continue
		}
		mountPoint := unescapeMountInfo(fields[4])
		options := fields[5]
		if !hasMountOption(options, "rw") {
			continue
		}
		return mountPoint, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("cgroup v2 is not mounted read-write")
}

func hasMountOption(options, want string) bool {
	for _, option := range strings.Split(options, ",") {
		if option == want {
			return true
		}
	}
	return false
}

func unescapeMountInfo(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func currentCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			path := strings.TrimPrefix(line, "0::")
			if path == "" {
				path = "/"
			}
			return path, nil
		}
	}
	return "", errors.New("current process is not in a cgroup v2 hierarchy")
}

func instanceCgroupPath(root string) (string, error) {
	cgroupInstance.once.Do(func() {
		start, _ := processStartTime(os.Getpid())
		if start == "" {
			start = "0"
		}
		name := fmt.Sprintf("picoclaw-%d-%s-%s", os.Getpid(), start, randomToken())
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o755); err != nil {
			cgroupInstance.err = err
			return
		}
		cgroupInstance.path = path
		go gcStaleCgroups(root, path)
	})
	if cgroupInstance.err != nil {
		return "", cgroupInstance.err
	}
	// The directory can disappear if an earlier scope removed an empty parent;
	// recreate it lazily.
	if err := os.MkdirAll(cgroupInstance.path, 0o755); err != nil {
		return "", err
	}
	return cgroupInstance.path, nil
}

func randomToken() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

func processStartTime(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	text := string(data)
	closeParen := strings.LastIndexByte(text, ')')
	if closeParen < 0 || closeParen+1 >= len(text) {
		return "", false
	}
	fields := strings.Fields(text[closeParen+1:])
	// fields[0] is state (field 3); starttime is field 22.
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}

func gcStaleCgroups(root, current string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "picoclaw-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if path == current {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(entry.Name(), "picoclaw-"), "-", 3)
		if len(parts) < 2 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil || pid <= 0 {
			continue
		}
		if start, ok := processStartTime(pid); ok && start == parts[1] {
			continue
		}
		removeCgroupTree(path)
	}
}

func removeCgroupTree(path string) {
	entries, err := os.ReadDir(path)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				removeCgroupTree(filepath.Join(path, entry.Name()))
			}
		}
	}
	_ = forceKillCgroup(path, 2*time.Second)
	// Real cgroupfs virtual files disappear with rmdir; best-effort removal
	// keeps tests and non-cgroup mock directories clean.
	if entries, err := os.ReadDir(path); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				_ = os.Remove(filepath.Join(path, entry.Name()))
			}
		}
	}
	_ = os.Remove(path)
}

type cgroupScope struct {
	cfg           Config
	dir           string
	fd            int
	killAvailable bool
	freeze        bool

	mu       sync.Mutex
	cmd      *exec.Cmd
	started  bool
	closed   bool
	killOnce sync.Once
	killErr  error
}

func (s *cgroupScope) Prepare(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("nil exec command")
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = s.fd
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	return nil
}

func (s *cgroupScope) Start(cmd *exec.Cmd) error {
	err := isolation.StartWithOptions(cmd, isolation.StartOptions{AsPID1: s.cfg.Foreground})
	if s.fd >= 0 {
		_ = unix.Close(s.fd)
		s.fd = -1
	}
	if err != nil {
		_ = s.Close()
		return err
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return nil
}

func (s *cgroupScope) Kill() error {
	s.killOnce.Do(func() {
		s.killErr = forceKillCgroup(s.dir, 5*time.Second)
	})
	return s.killErr
}

func (s *cgroupScope) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	started := s.started
	fd := s.fd
	s.fd = -1
	s.mu.Unlock()

	if fd >= 0 {
		_ = unix.Close(fd)
	}
	if started && !s.Empty() {
		_ = s.Kill()
	}
	// Wait for the cgroup to drain before removing it. If a process is stuck in
	// an uninterruptible sleep leave the directory for the startup GC.
	deadline := time.Now().Add(2 * time.Second)
	for !s.Empty() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Empty() {
		_ = os.Remove(s.dir)
	}
	return nil
}

func (s *cgroupScope) MarkExited() {}

func (s *cgroupScope) Empty() bool {
	if s.dir == "" {
		return true
	}
	populated, err := cgroupPopulated(s.dir)
	if err != nil {
		return true
	}
	return !populated
}

func (s *cgroupScope) Backend() string {
	return BackendCgroup
}

func forceKillCgroup(dir string, timeout time.Duration) error {
	if dir == "" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	if _, statErr := os.Stat(filepath.Join(dir, "cgroup.kill")); statErr == nil {
		if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0); err == nil {
			return waitCgroupEmpty(dir, deadline)
		}
	}

	// Kernels before 5.14 do not have cgroup.kill. Freeze the subtree so new
	// forks stop, then kill every PID listed in cgroup.procs.
	if _, statErr := os.Stat(filepath.Join(dir, "cgroup.freeze")); statErr == nil {
		_ = os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte("1"), 0)
	}
	for {
		pids := cgroupProcs(dir)
		if len(pids) == 0 {
			return nil
		}
		for _, pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if time.Now().After(deadline) {
			_ = os.WriteFile(filepath.Join(dir, "cgroup.freeze"), []byte("0"), 0)
			return fmt.Errorf("timed out killing cgroup %s", dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitCgroupEmpty(dir string, deadline time.Time) error {
	for {
		populated, err := cgroupPopulated(dir)
		if err != nil || !populated {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for cgroup %s to become empty", dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cgroupPopulated(dir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.events"))
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "populated" {
			return fields[1] == "1", nil
		}
	}
	return false, errors.New("cgroup.events has no populated field")
}

func cgroupProcs(dir string) []int {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(data), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}
