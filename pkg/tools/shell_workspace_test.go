package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newWorkspaceGuardTestTool(t *testing.T, workspace string) *ExecTool {
	t.Helper()

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				// Exercise the workspace AST layer in isolation: with the
				// default deny set enabled, several of these commands would be
				// rejected earlier for unrelated reasons.
				EnableDenyPatterns: false,
				AllowRemote:        true,
			},
		},
	}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	return tool
}

func runWorkspaceGuardCommand(t *testing.T, tool *ExecTool, command string) *ToolResult {
	t.Helper()
	return tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": command,
	})
}

func TestShellTool_WorkspaceGuardDefaultConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST workspace guard")
	}

	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)

	blocked := []string{
		"cat ~/.ssh/id_rsa",
		"cat $HOME/.ssh/id_rsa",
		"echo pwn > $HOME/pwn",
		"cd .. && touch sibling",
		"pushd $HOME",
		"ln -s .. escape",
	}
	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.True(t, result.IsError, "expected %q to be blocked: %s", command, result.ForLLM)
			require.Contains(t, result.ForLLM, "blocked by safety guard",
				"expected workspace guard to block %q: %s", command, result.ForLLM)
		})
	}
}

func TestShellTool_WorkspaceHomeExpansionBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST workspace guard")
	}

	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	tool := newWorkspaceGuardTestTool(t, workspace)

	blocked := []string{
		"cat ~/.ssh/id_rsa",
		"cat $HOME/.ssh/id_rsa",
		"cat ${HOME}/.ssh/id_rsa",
		`cat "$HOME"/.ssh/id_rsa`,
		"cat ~root/.ssh/id_rsa",
		"echo pwn > $HOME/pwn",
		"echo pwn > ~/pwn",
		"env --chdir=$HOME true",
		"cd ~",
		"cd $HOME",
		"sh -c 'cat ~/.ssh/id_rsa'",
		`bash -c 'cat $HOME/.ssh/id_rsa'`,
		"eval 'cat ~/.ssh/id_rsa'",
	}

	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.True(t, result.IsError, "expected %q to be blocked: %s", command, result.ForLLM)
			require.Contains(t, result.ForLLM, "path outside working dir",
				"expected workspace guard to block %q: %s", command, result.ForLLM)
		})
	}

	_, err := os.Stat(filepath.Join(home, "pwn"))
	require.True(t, os.IsNotExist(err), "blocked command created a file in the home directory")
}

func TestShellTool_WorkspaceDirectoryChangeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST workspace guard")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	t.Setenv("HOME", filepath.Join(root, "home"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "home"), 0o755))

	tool := newWorkspaceGuardTestTool(t, workspace)

	blocked := []string{
		"cd ..",
		"cd .. && touch sibling",
		"cd ../outside",
		"cd ~",
		"cd $HOME",
		"cd -",
		"pushd ..",
		"pushd $HOME",
		"env -C .. touch sibling",
		"env --chdir=../outside touch sibling",
		"sh -c 'cd .. && touch sibling'",
		"bash -c 'cd $HOME && touch pwn'",
		"eval 'cd ..'",
		"exec bash -c 'cd ~'",
		"find . -exec sh -c 'cd ~' {} \\;",
		"env -S 'cd ~'",
	}

	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.True(t, result.IsError, "expected %q to be blocked: %s", command, result.ForLLM)
			require.Contains(t, result.ForLLM, "blocked by safety guard",
				"expected workspace guard to block %q: %s", command, result.ForLLM)
		})
	}

	for _, path := range []string{
		filepath.Join(root, "sibling"),
		filepath.Join(outside, "sibling"),
		filepath.Join(outside, "pwn"),
	} {
		_, err := os.Stat(path)
		require.True(t, os.IsNotExist(err), "blocked command created %s", path)
	}

	allowed := []string{
		"cd sub",
		"cd ./sub",
		"cd " + filepath.Join(workspace, "sub"),
		"command cd sub",
		"builtin cd sub",
		"pushd sub",
		"pushd sub && popd",
		"env -C sub true",
		"sh -c 'cd sub && echo ok'",
		"exec true",
		"find . -exec sh -c 'cd sub && echo ok' {} \\;",
	}
	for _, command := range allowed {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.NotContains(t, result.ForLLM, "blocked by safety guard",
				"expected %q to be allowed: %s", command, result.ForLLM)
		})
	}
}

func TestShellTool_WorkspaceCDSymlinkEscapeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink semantics")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("CANARY_SECRET_12345\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "delete-me.txt"), []byte("delete me\n"), 0o600))
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	tool := newWorkspaceGuardTestTool(t, workspace)

	blocked := []string{
		"cd link && touch pwn",
		"cd link; cat secret.txt",
		"cd link; echo PWNED > pwn2.txt",
		"cd link; rm -f delete-me.txt",
		"pushd link",
		"env -C link touch pwn",
		"cd link/../outside",
	}
	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.True(t, result.IsError, "expected %q to be blocked: %s", command, result.ForLLM)
			require.Contains(t, result.ForLLM, "blocked by safety guard",
				"expected workspace guard to block %q: %s", command, result.ForLLM)
		})
	}

	for _, path := range []string{"pwn", "pwn2.txt"} {
		_, err := os.Stat(filepath.Join(outside, path))
		require.True(t, os.IsNotExist(err), "blocked command created %s outside the workspace", path)
	}
	_, err := os.Stat(filepath.Join(outside, "delete-me.txt"))
	require.NoError(t, err, "blocked command deleted the outside canary")
	data, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	require.NoError(t, err)
	require.Equal(t, "CANARY_SECRET_12345\n", string(data), "blocked command modified the outside canary")
}

func TestShellTool_WorkspaceHomeInsideWorkspaceAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST workspace guard")
	}

	workspace := t.TempDir()
	t.Setenv("HOME", workspace)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(workspace, "inside.txt"), []byte("home-ok\n"), 0o644),
	)
	tool := newWorkspaceGuardTestTool(t, workspace)

	for _, command := range []string{"cat ~/inside.txt", "cat $HOME/inside.txt"} {
		result := runWorkspaceGuardCommand(t, tool, command)
		require.False(t, result.IsError, "%q should be allowed: %s", command, result.ForLLM)
		require.Contains(t, result.ForLLM, "home-ok")
	}
}

func TestShellTool_WorkspaceDynamicRedirectBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST workspace guard")
	}

	workspace := t.TempDir()
	tool := newWorkspaceGuardTestTool(t, workspace)

	result := runWorkspaceGuardCommand(t, tool, `echo pwn > "$OUT"`)
	require.True(t, result.IsError)
	require.Contains(t, result.ForLLM, "dynamic path cannot be validated")
	require.NotContains(t, strings.ToLower(result.ForLLM), "not a directory")
}

// TestShellTool_WorkspaceEffectiveCwdEscapeBlocked covers the second step of
// the cd escape: the first cd stays inside the workspace, but a later cd or a
// relative operand inside that directory reaches a symlink pointing outside.
// Validating the first cd alone is not enough; every relative operand must be
// resolved against the directory the shell actually changed into.
func TestShellTool_WorkspaceEffectiveCwdEscapeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink semantics")
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	sub := filepath.Join(workspace, "sub")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(sub, "nested"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("CANARY_SECRET_12345\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "delete-me.txt"), []byte("delete me\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "inside.txt"), []byte("inside-ok\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "nested", "inside.txt"), []byte("nested-ok\n"), 0o600))
	if err := os.Symlink(outside, filepath.Join(sub, "outside-link")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(sub, "secret")); err != nil {
		t.Fatal(err)
	}

	tool := newWorkspaceGuardTestTool(t, workspace)

	blocked := []string{
		"cd sub; cat outside-link/secret.txt",
		"cd sub; echo PWNED > outside-link/pwn.txt",
		"cd sub; rm -f outside-link/delete-me.txt",
		"cd sub; cat secret",
		"cd sub; cd outside-link; cat secret.txt",
		"cd sub; cd outside-link; echo PWNED > pwn.txt",
		"env -C sub cat outside-link/secret.txt",
		"sh -c 'cd sub; cat outside-link/secret.txt'",
		"cd sub; sh -c 'cat secret'",
	}
	for _, command := range blocked {
		t.Run(command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, command)
			require.True(t, result.IsError, "expected %q to be blocked: %s", command, result.ForLLM)
			require.Contains(t, result.ForLLM, "blocked by safety guard",
				"expected workspace guard to block %q: %s", command, result.ForLLM)
		})
	}

	allowed := []struct {
		command string
		content string
	}{
		{"cd sub; cat inside.txt", "inside-ok"},
		{"cd sub; cd nested; cat inside.txt", "nested-ok"},
		{"env -C sub cat inside.txt", "inside-ok"},
	}
	for _, tc := range allowed {
		t.Run(tc.command, func(t *testing.T) {
			result := runWorkspaceGuardCommand(t, tool, tc.command)
			require.False(t, result.IsError, "expected %q to be allowed: %s", tc.command, result.ForLLM)
			require.Contains(t, result.ForLLM, tc.content,
				"expected %q to read inside the workspace: %s", tc.command, result.ForLLM)
		})
	}

	data, err := os.ReadFile(filepath.Join(outside, "secret.txt"))
	require.NoError(t, err)
	require.Equal(t, "CANARY_SECRET_12345\n", string(data), "outside secret was modified")
	_, err = os.Stat(filepath.Join(outside, "delete-me.txt"))
	require.NoError(t, err, "outside canary was deleted")
	_, err = os.Stat(filepath.Join(outside, "pwn.txt"))
	require.True(t, os.IsNotExist(err), "a blocked command created a file outside the workspace")
}
