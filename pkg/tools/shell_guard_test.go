package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// externalExecutable creates a workspace with .venv/bin/python symlinked to an
// executable outside it, mirroring how virtualenvs and version managers expose
// their interpreters.
func externalExecutable(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink executable layout is Unix-specific")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	binDir := filepath.Join(workspace, ".venv", "bin")
	for _, dir := range []string{binDir, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	target := filepath.Join(outside, "python")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho toolchain ok\n"), 0o755); err != nil {
		t.Fatalf("write outside executable: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(binDir, "python")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	return workspace
}

func newGuardTool(t *testing.T, workspace string) *ExecTool {
	t.Helper()
	// Disable deny patterns so these tests exercise the workspace guard rather
	// than the unrelated command denylist (which rejects sudo and $()).
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = false
	cfg.Tools.Exec.AllowRemote = true
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	return tool
}

func TestShellTool_ExecutableSymlinkInsideWorkspaceRuns(t *testing.T) {
	workspace := externalExecutable(t)
	tool := newGuardTool(t, workspace)

	for _, command := range []string{
		".venv/bin/python -V",
		"./.venv/bin/python -V",
		"true && .venv/bin/python",
		"false || .venv/bin/python",
		"true; .venv/bin/python",
		"command .venv/bin/python",
		"env FOO=1 .venv/bin/python",
	} {
		result := tool.Execute(context.Background(), map[string]any{
			"action": "run", "command": command,
		})
		if result.IsError {
			t.Errorf("%q should run an in-workspace executable symlink, got: %s", command, result.ForLLM)
			continue
		}
		if !strings.Contains(result.ForLLM, "toolchain ok") {
			t.Errorf("%q did not reach the symlink target: %s", command, result.ForLLM)
		}
	}
}

func TestShellTool_ExecutableSymlinkInRelativeCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink executable layout is Unix-specific")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	sandbox := filepath.Join(workspace, "sandbox")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{filepath.Join(sandbox, ".venv", "bin"), outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	target := filepath.Join(outside, "python")
	if err := os.WriteFile(target, []byte("#!/bin/sh\necho sandbox ok\n"), 0o755); err != nil {
		t.Fatalf("write outside executable: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(sandbox, ".venv", "bin", "python")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	tool := newGuardTool(t, workspace)
	result := tool.Execute(context.Background(), map[string]any{
		"action": "run", "command": ".venv/bin/python", "cwd": sandbox,
	})
	if result.IsError {
		t.Fatalf("relative executable in cwd should run, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "sandbox ok") {
		t.Fatalf("unexpected output: %s", result.ForLLM)
	}
}

func TestShellGuard_ExecutableSymlinkPositionsAllowed(t *testing.T) {
	workspace := externalExecutable(t)
	tool := newGuardTool(t, workspace)

	for _, command := range []string{
		".venv/bin/python",
		".venv/bin/python;true",
		"env -i .venv/bin/python",
		"env -u NAME .venv/bin/python",
		"env --chdir=. .venv/bin/python",
		"timeout -s KILL 5 .venv/bin/python",
		"timeout --signal=KILL 5 .venv/bin/python",
		"nice -n 5 .venv/bin/python",
		"nice --adjustment=5 .venv/bin/python",
		"nohup .venv/bin/python",
		"stdbuf -o0 .venv/bin/python",
		"setsid .venv/bin/python",
		"sudo -u root .venv/bin/python",
		"sudo -E .venv/bin/python",
		"chroot . .venv/bin/python",
		"chroot --userspec=0:0 . .venv/bin/python",
		"runuser -u root -- .venv/bin/python",
		"time .venv/bin/python",
		"! .venv/bin/python",
		"if .venv/bin/python; then true; fi",
		"while .venv/bin/python; do true; done",
		"env -- .venv/bin/python",
	} {
		if got := tool.guardCommand(command, workspace); got != "" {
			t.Errorf("%q should be treated as an executable position, got: %s", command, got)
		}
	}
}

func TestShellGuard_SymlinkOperandsStillBlocked(t *testing.T) {
	workspace := externalExecutable(t)
	tool := newGuardTool(t, workspace)

	for _, command := range []string{
		"cat .venv/bin/python",
		".venv/bin/python;cat .venv/bin/python",
		"echo .venv/bin/python",
		"echo > .venv/bin/python",
		"echo 2>.venv/bin/python",
		"echo <> .venv/bin/python",
		"echo --config=.venv/bin/python",
		"env -u .venv/bin/python true",
		"env -C .venv/bin/python true",
		"env -S .venv/bin/python true",
		"timeout -s .venv/bin/python 5 true",
		"sudo -u .venv/bin/python true",
		"sudo -e .venv/bin/python",
		"sudo --unknown .venv/bin/python",
		"nice -5 .venv/bin/python",
		"xargs .venv/bin/python",
		"find . -exec .venv/bin/python {} \\;",
		"sh -c '.venv/bin/python'",
		"for x in a; do .venv/bin/python; done",
	} {
		got := tool.guardCommand(command, workspace)
		if !strings.Contains(got, "path outside working dir") {
			t.Errorf("%q should be blocked as an operand, got: %q", command, got)
		}
	}
}
