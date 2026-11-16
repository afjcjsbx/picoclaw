//go:build linux

package execscope

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorKillsTreeOnControlEOF(t *testing.T) {
	setsidPath, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid binary is not available")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	pidFile := filepath.Join(t.TempDir(), "child.pid")
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlWrite.Close()

	command := fmt.Sprintf("%q sleep 60 & echo $! > %q; wait", setsidPath, pidFile)
	cmd := exec.Command(executable, supervisorArg, "--control-fd", "3", "--", "/bin/sh", "-c", command)
	cmd.ExtraFiles = []*os.File{controlRead}
	if err := cmd.Start(); err != nil {
		_ = controlRead.Close()
		t.Fatalf("start supervisor: %v", err)
	}
	_ = controlRead.Close()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	pid := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	// Simulate the agent dying: the supervisor sees EOF on its control pipe and
	// must kill the whole subtree before exiting.
	_ = controlWrite.Close()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not exit after control pipe EOF")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("detached child %d survived supervisor control EOF", pid)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pid file %s was never written", path)
	return 0
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
