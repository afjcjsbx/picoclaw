package shellguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testWorkspacePolicy(t *testing.T) (WorkspacePolicy, string, string) {
	t.Helper()

	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}

	policy := WorkspacePolicy{
		Cwd:  cwd,
		Home: home,
		AllowPath: func(path string) bool {
			rel, err := filepath.Rel(cwd, path)
			return err == nil && !strings.HasPrefix(rel, "..")
		},
	}
	return policy, cwd, home
}

func TestCheckWorkspaceBlocksEscapes(t *testing.T) {
	t.Parallel()

	policy, _, _ := testWorkspacePolicy(t)
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "tilde", command: "cat ~/.ssh/id_rsa", want: "path outside working dir"},
		{name: "home", command: "cat $HOME/.ssh/id_rsa", want: "path outside working dir"},
		{name: "home braces", command: "cat ${HOME}/.ssh/id_rsa", want: "path outside working dir"},
		{name: "home modifier", command: "cat ${HOME:-/tmp}/x", want: "path outside working dir"},
		{name: "home quoted", command: `cat "$HOME"/.ssh/id_rsa`, want: "path outside working dir"},
		{name: "tilde redirect", command: "echo pwn > ~/pwn", want: "path outside working dir"},
		{name: "home redirect", command: "echo pwn > $HOME/pwn", want: "path outside working dir"},
		{name: "tilde user", command: "cat ~root/.ssh/id_rsa", want: "path outside working dir"},
		{name: "tilde assignment", command: "X=~/x true", want: "path outside working dir"},
		{name: "tilde declaration", command: "export X=~/x", want: "path outside working dir"},
		{name: "cd parent", command: "cd ..", want: "path traversal detected"},
		{
			name:    "cd parent chain",
			command: "cd .. && touch sibling",
			want:    "path traversal detected",
		},
		{name: "ansi-c cd parent", command: `c$'\x64' ..`, want: "path traversal detected"},
		{name: "ansi-c cd home", command: `c$'\x64' ~`, want: "path outside working dir"},
		{name: "nested ansi-c cd", command: `sh -c "c$'\x64' .."`, want: "path traversal detected"},
		{name: "pushd home", command: "pushd ~", want: "path outside working dir"},
		{name: "cd oldpwd", command: "cd -", want: "directory change cannot be validated"},
		{name: "cd dynamic", command: "cd $DIR", want: "directory change cannot be validated"},
		{
			name:    "env chdir parent",
			command: "env -C .. touch sibling",
			want:    "path traversal detected",
		},
		{
			name:    "env chdir home",
			command: "env --chdir=$HOME touch pwn",
			want:    "path outside working dir",
		},
		{
			name:    "nested shell cd",
			command: "sh -c 'cd .. && touch sibling'",
			want:    "path traversal detected",
		},
		{
			name:    "nested shell home",
			command: "bash -c 'cat $HOME/x'",
			want:    "path outside working dir",
		},
		{name: "herestring shell cd", command: "sh <<< 'cd ~'", want: "path outside working dir"},
		{
			name:    "heredoc shell cd",
			command: "bash <<'EOF'\ncd ~\nEOF",
			want:    "path outside working dir",
		},
		{
			name:    "exec nested shell",
			command: "exec bash -c 'cd ~'",
			want:    "path outside working dir",
		},
		{
			name:    "busybox nested shell",
			command: "busybox sh -c 'cd ~'",
			want:    "path outside working dir",
		},
		{
			name:    "find exec nested shell",
			command: "find . -exec sh -c 'cd ~' {} \\;",
			want:    "path outside working dir",
		},
		{
			name:    "find exec dynamic chdir",
			command: "find . -exec env -C $DIR true {} \\;",
			want:    "directory change cannot be validated",
		},
		{
			name:    "test clause home",
			command: "[[ -f ~/.ssh/id_rsa ]]",
			want:    "path outside working dir",
		},
		{
			name:    "for loop home",
			command: `for f in ~/.ssh/*; do echo "$f"; done`,
			want:    "path outside working dir",
		},
		{name: "array home", command: "arr=(~/.ssh/id_rsa)", want: "path outside working dir"},
		{name: "array absolute", command: "arr=(/etc/passwd)", want: "path outside working dir"},
		{name: "symlink parent", command: "ln -s .. escape", want: "path traversal detected"},
		{
			name:    "wrapped symlink parent",
			command: "command ln -s .. escape",
			want:    "path traversal detected",
		},
		{name: "env split string", command: "env -S 'cd ~'", want: "env -S cannot be validated"},
		{name: "eval cd", command: "eval 'cd ..'", want: "path traversal detected"},
		{
			name:    "dynamic redirect",
			command: "echo pwn > $OUT",
			want:    "dynamic path cannot be validated",
		},
		{
			name:    "home assignment",
			command: "HOME=/elsewhere; cd ~",
			want:    "path outside working dir",
		},
		{
			name:    "dynamic home assignment",
			command: "HOME=$X; cd ~",
			want:    "path outside working dir",
		},
		{
			name:    "cdpath assignment",
			command: "CDPATH=/elsewhere; cd sub",
			want:    "path outside working dir",
		},
		{
			name:    "dynamic cdpath assignment",
			command: "CDPATH=$X; cd sub",
			want:    "dynamic path assignment",
		},
		{
			name:    "env home override",
			command: "env HOME=/elsewhere sh -c 'cd ~'",
			want:    "path environment override",
		},
		{
			name:    "path flag parent",
			command: "tar -C .. -cf out.tar .",
			want:    "path traversal detected",
		},
		{
			name:    "git path flag parent",
			command: "git -C .. status",
			want:    "path traversal detected",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := CheckWorkspace(tc.command, policy)
			if err == nil {
				t.Fatalf("CheckWorkspace(%q) = nil, want %q", tc.command, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckWorkspace(%q) = %q, want %q", tc.command, err, tc.want)
			}
		})
	}
}

func TestCheckWorkspaceAllowsInsideCommands(t *testing.T) {
	t.Parallel()

	policy, cwd, _ := testWorkspacePolicy(t)
	commands := []string{
		"cat ./file",
		"cd sub",
		"cd ./sub",
		"cd " + filepath.Join(cwd, "sub"),
		"pushd sub && popd",
		"command cd sub",
		"builtin cd sub",
		"env -C sub true",
		"sh -c 'cd sub && echo ok'",
		"find . -exec sh -c 'cd sub && echo ok' {} \\;",
		"busybox sh -c 'cd sub && echo ok'",
		"echo pwn > out.txt",
		"git log HEAD..main",
		"echo '$HOME'",
		"echo '~'",
		"echo X=~/foo",
		"echo ..",
		"grep .. file",
		"tar -C sub -cf out.tar .",
		"bash script.sh <<'EOF'\ncd ~\nEOF",
		"[[ -f ./file ]]",
		`for f in ./files/*; do echo "$f"; done`,
		"arr=(./file)",
		"ln -s ./sub escape",
		"echo https://example.com/a/../b",
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			if err := CheckWorkspace(command, policy); err != nil {
				t.Fatalf("CheckWorkspace(%q) = %v, want nil", command, err)
			}
		})
	}
}

func TestCheckWorkspaceUnsetHome(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	policy := WorkspacePolicy{
		Cwd:  cwd,
		Home: cwd,
		AllowPath: func(path string) bool {
			rel, err := filepath.Rel(cwd, path)
			return err == nil && !strings.HasPrefix(rel, "..")
		},
	}

	if err := CheckWorkspace("unset HOME; cd ~", policy); err == nil ||
		!strings.Contains(err.Error(), "path outside working dir") {
		t.Fatalf("cd after unset HOME should be rejected, got: %v", err)
	}
	if err := CheckWorkspace("unset HOME; sh -c 'cat $HOME/file'", policy); err == nil ||
		!strings.Contains(err.Error(), "path outside working dir") {
		t.Fatalf("nested $HOME after unset HOME should be rejected, got: %v", err)
	}
	if err := CheckWorkspace("unset PWD; eval 'cat $PWD/etc/passwd'", policy); err == nil ||
		!strings.Contains(err.Error(), "path outside working dir") {
		t.Fatalf("nested $PWD after unset PWD should be rejected, got: %v", err)
	}
	if err := CheckWorkspace("cd ~", policy); err != nil {
		t.Fatalf("cd ~ with HOME inside the workspace should be allowed, got: %v", err)
	}
	if err := CheckWorkspace("HOME=.; sh -c 'cat $HOME/file'", policy); err == nil {
		t.Fatal("a HOME assignment must conservatively disable tilde expansion")
	}
	for _, command := range []string{
		"(HOME=.); cd ~",
		"f() { HOME=.; }; cd ~",
		"if false; then HOME=.; fi; cd ~",
		"env -i sh -c 'cat $HOME/file'",
		"env -u HOME sh -c 'cat $HOME/file'",
	} {
		if err := CheckWorkspace(command, policy); err == nil {
			t.Fatalf("HOME assignment in a nested scope must not loosen %q", command)
		}
	}
}

func TestCheckWorkspaceCDPath(t *testing.T) {
	t.Parallel()

	policy, cwd, _ := testWorkspacePolicy(t)
	policy.CDPath = true

	if err := CheckWorkspace("cd sub", policy); err == nil ||
		!strings.Contains(err.Error(), "directory change cannot be validated") {
		t.Fatalf("relative cd with CDPATH should be rejected, got: %v", err)
	}
	for _, command := range []string{"cd ./sub", "cd " + filepath.Join(cwd, "sub")} {
		if err := CheckWorkspace(command, policy); err != nil {
			t.Fatalf("CheckWorkspace(%q) = %v, want nil", command, err)
		}
	}
}

func TestCheckWorkspaceEffectiveCwd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	sub := filepath.Join(cwd, "sub")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatalf("create subdirs: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("create outside: %v", err)
	}
	// The production guard resolves the workspace root before calling
	// CheckWorkspace; resolve it here for the same comparison semantics.
	resolvedCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	cwd = resolvedCwd
	if err := os.WriteFile(filepath.Join(sub, "inside.txt"), []byte("inside\n"), 0o600); err != nil {
		t.Fatalf("write inside file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(sub, "outside-link")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(sub, "secret")); err != nil {
		t.Fatalf("create secret symlink: %v", err)
	}

	policy := WorkspacePolicy{
		Cwd: cwd,
		AllowPath: func(path string) bool {
			resolved := path
			for {
				if evaluated, err := filepath.EvalSymlinks(resolved); err == nil {
					resolved = evaluated
					break
				}
				parent := filepath.Dir(resolved)
				if parent == resolved {
					break
				}
				resolved = parent
			}
			rel, err := filepath.Rel(cwd, resolved)
			return err == nil && !strings.HasPrefix(rel, "..")
		},
	}

	for _, command := range []string{
		"cd sub; cat outside-link/secret.txt",
		"cd sub; echo pwn > outside-link/pwn.txt",
		"cd sub; cd outside-link; cat secret.txt",
		"env -C sub cat outside-link/secret.txt",
		"sh -c 'cd sub; cat outside-link/secret.txt'",
		"cd sub; sh -c 'cat secret'",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			if err := CheckWorkspace(command, policy); err == nil {
				t.Fatalf("CheckWorkspace(%q) = nil, want a workspace escape", command)
			}
		})
	}

	for _, command := range []string{
		"cd sub; cat inside.txt",
		"cd sub; cd nested; cat inside.txt",
		"env -C sub cat inside.txt",
		"sh -c 'cd sub; cat inside.txt'",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			if err := CheckWorkspace(command, policy); err != nil {
				t.Fatalf("CheckWorkspace(%q) = %v, want nil", command, err)
			}
		})
	}
}

func TestCheckWorkspaceAllowsHomeInsideWorkspace(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	policy := WorkspacePolicy{
		Cwd:  cwd,
		Home: cwd,
		AllowPath: func(path string) bool {
			rel, err := filepath.Rel(cwd, path)
			return err == nil && !strings.HasPrefix(rel, "..")
		},
	}

	for _, command := range []string{
		"cat ~/file",
		"cat $HOME/file",
		`cd "~"`,
	} {
		if err := CheckWorkspace(command, policy); err != nil {
			t.Fatalf("CheckWorkspace(%q) = %v, want nil", command, err)
		}
	}
}
