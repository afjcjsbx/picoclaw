package isolation

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolvedBackendSSH(t *testing.T) {
	if got := resolvedBackend(config.IsolationConfig{Backend: "ssh"}); got != config.IsolationBackendSSH {
		t.Fatalf("resolvedBackend(ssh) = %q, want %q", got, config.IsolationBackendSSH)
	}
	if !isBackendSupportedOn("linux", config.IsolationBackendSSH) {
		t.Fatal("ssh backend should be supported on linux")
	}
	if !isBackendSupportedOn("darwin", config.IsolationBackendSSH) {
		t.Fatal("ssh backend should be supported on darwin")
	}
	if isBackendSupportedOn("windows", config.IsolationBackendSSH) {
		t.Fatal("ssh backend should not be supported on windows")
	}
}

func TestValidateSSHConfig(t *testing.T) {
	valid := config.SSHIsolationConfig{
		Host:       "sandbox.internal",
		User:       "worker",
		Port:       2222,
		KeyPath:    "~/.ssh/id_ed25519",
		RemoteRoot: "/srv/picoclaw",
		ForwardEnv: []string{"GOPATH", "LC_*"},
		ExtraArgs:  []string{"-o", "ProxyJump=bastion", "-4C"},
	}
	if err := validateSSHConfig(valid); err != nil {
		t.Fatalf("validateSSHConfig(valid) = %v", err)
	}
	combined := config.SSHIsolationConfig{
		Host:      "sandbox.internal",
		ExtraArgs: []string{"-oProxyJump=bastion"},
	}
	if err := validateSSHConfig(combined); err != nil {
		t.Fatalf("validateSSHConfig(combined -o) = %v", err)
	}
	if err := validateSSHConfig(config.SSHIsolationConfig{Host: "worker@sandbox"}); err != nil {
		t.Fatalf("validateSSHConfig(pasted target) = %v", err)
	}

	tests := []struct {
		name string
		cfg  config.SSHIsolationConfig
	}{
		{name: "missing host", cfg: config.SSHIsolationConfig{}},
		{name: "host starts with dash", cfg: config.SSHIsolationConfig{Host: "-host"}},
		{name: "host with space", cfg: config.SSHIsolationConfig{Host: "bad host"}},
		{name: "user with space", cfg: config.SSHIsolationConfig{Host: "host", User: "bad user"}},
		{name: "user with at", cfg: config.SSHIsolationConfig{Host: "host", User: "a@b"}},
		{
			name: "host with at and user",
			cfg:  config.SSHIsolationConfig{Host: "a@host", User: "worker"},
		},
		{name: "negative port", cfg: config.SSHIsolationConfig{Host: "host", Port: -1}},
		{name: "large port", cfg: config.SSHIsolationConfig{Host: "host", Port: 70000}},
		{
			name: "key path newline",
			cfg:  config.SSHIsolationConfig{Host: "host", KeyPath: "key\npath"},
		},
		{
			name: "relative remote root",
			cfg:  config.SSHIsolationConfig{Host: "host", RemoteRoot: "srv/picoclaw"},
		},
		{
			name: "empty forward env",
			cfg:  config.SSHIsolationConfig{Host: "host", ForwardEnv: []string{""}},
		},
		{
			name: "forward env with equals",
			cfg:  config.SSHIsolationConfig{Host: "host", ForwardEnv: []string{"FOO=bar"}},
		},
		{
			name: "reserved forward env",
			cfg:  config.SSHIsolationConfig{Host: "host", ForwardEnv: []string{"HOME"}},
		},
		{
			name: "internal forward env",
			cfg:  config.SSHIsolationConfig{Host: "host", ForwardEnv: []string{"PICOCLAW_*"}},
		},
		{
			name: "empty extra arg",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{""}},
		},
		{
			name: "end of options",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"--"}},
		},
		{
			name: "control command",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"-O", "exit"}},
		},
		{
			name: "port override",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"-p", "22"}},
		},
		{
			name: "agent forwarding",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"-A"}},
		},
		{
			name: "local forward",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"-L", "8080:localhost:80"}},
		},
		{
			name: "force tty",
			cfg:  config.SSHIsolationConfig{Host: "host", ExtraArgs: []string{"-t"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSSHConfig(tt.cfg); err == nil {
				t.Fatal("validateSSHConfig() = nil, want error")
			}
		})
	}
}

func TestValidateSSHIsolationRejectsExposePaths(t *testing.T) {
	err := validateSSHIsolation(config.IsolationConfig{
		Enabled:     true,
		Backend:     config.IsolationBackendSSH,
		ExposePaths: []config.ExposePath{{Source: "/tmp", Target: "/tmp", Mode: "ro"}},
		SSH:         config.SSHIsolationConfig{Host: "host"},
	})
	if err == nil || !strings.Contains(err.Error(), "expose_paths") {
		t.Fatalf("validateSSHIsolation() = %v, want expose_paths error", err)
	}
}

func TestSSHRemotePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	root := filepath.Clean("/home/worker/.picoclaw")
	tests := []struct {
		name       string
		path       string
		remoteRoot string
		want       string
	}{
		{name: "root maps to remote root", path: root, remoteRoot: "/srv/picoclaw", want: "/srv/picoclaw"},
		{
			name:       "workspace maps under remote root",
			path:       filepath.Join(root, "workspace"),
			remoteRoot: "/srv/picoclaw",
			want:       filepath.Join("/srv/picoclaw", "workspace"),
		},
		{
			name:       "outside root keeps path",
			path:       "/tmp/output.txt",
			remoteRoot: "/srv/picoclaw",
			want:       "/tmp/output.txt",
		},
		{
			name: "empty remote root keeps path",
			path: filepath.Join(root, "workspace"),
			want: filepath.Join(root, "workspace"),
		},
		{name: "empty path", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sshRemotePath(tt.path, root, tt.remoteRoot); got != tt.want {
				t.Fatalf("sshRemotePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSSHRemoteCommandPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	root := filepath.Clean("/home/worker/.picoclaw")
	if got := sshRemoteCommandPath(
		filepath.Join(root, "bin", "tool"),
		"/srv/picoclaw/workspace",
		root,
		"/srv/picoclaw",
	); got != "/srv/picoclaw/bin/tool" {
		t.Fatalf("absolute command path = %q, want /srv/picoclaw/bin/tool", got)
	}
	if got := sshRemoteCommandPath(
		"./scripts/run.sh",
		"/srv/picoclaw/workspace",
		root,
		"/srv/picoclaw",
	); got != "/srv/picoclaw/workspace/scripts/run.sh" {
		t.Fatalf("relative command path = %q, want remote workspace path", got)
	}
	if got := sshRemoteCommandPath("node", "/srv/picoclaw/workspace", root, "/srv/picoclaw"); got != "node" {
		t.Fatalf("bare command path = %q, want node", got)
	}
	if got := sshRemoteCommandPath("/usr/bin/sh", "", root, "/srv/picoclaw"); got != "/bin/sh" {
		t.Fatalf("system sh path = %q, want /bin/sh", got)
	}
	workspaceSh := filepath.Join(root, "bin", "sh")
	if got := sshRemoteCommandPath(workspaceSh, "", root, "/srv/picoclaw"); got != "/srv/picoclaw/bin/sh" {
		t.Fatalf("workspace sh path = %q, want mapped workspace path", got)
	}
	if got := sshRemoteCommandPath("/bin/bash", "", root, "/srv/picoclaw"); got != "/bin/bash" {
		t.Fatalf("bash path = %q, want /bin/bash", got)
	}
}

func TestSSHRemoteRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	if got := sshRemoteRoot("/home/worker/.picoclaw", ""); got != filepath.Clean("/home/worker/.picoclaw") {
		t.Fatalf("sshRemoteRoot() = %q, want local root", got)
	}
	if got := sshRemoteRoot("/home/worker/.picoclaw", "/srv/picoclaw/"); got != "/srv/picoclaw" {
		t.Fatalf("sshRemoteRoot() = %q, want /srv/picoclaw", got)
	}
}

func TestSSHShellQuote(t *testing.T) {
	tests := map[string]string{
		"":            "''",
		"abc/def-1":   "abc/def-1",
		"a b":         "'a b'",
		"a'b":         `'a'\''b'`,
		"$HOME":       "'$HOME'",
		"line\nbreak": "'line\nbreak'",
	}
	for input, want := range tests {
		if got := sshShellQuote(input); got != want {
			t.Errorf("sshShellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildSSHRemoteCommandRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("remote command quoting is POSIX")
	}
	dir := filepath.Join(t.TempDir(), "work space'quoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"a b", "c'd", `e"f`, "$HOME", "line\nbreak"}
	remote := buildSSHRemoteCommand(dir, "/bin/echo", args)
	cmd := exec.Command("/bin/sh", "-c", remote)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remote command round trip failed: %v\n%s", err, output)
	}
	want := strings.Join(args, " ") + "\n"
	if string(output) != want {
		t.Fatalf("round trip output = %q, want %q\ncommand: %s", output, want, remote)
	}
}

func TestBuildSSHArgs(t *testing.T) {
	cfg := config.SSHIsolationConfig{
		Host:      "sandbox.internal",
		User:      "worker",
		Port:      2222,
		KeyPath:   "/keys/id_ed25519",
		ExtraArgs: []string{"-o", "ProxyJump=bastion"},
	}
	args := buildSSHArgs(cfg, []string{"GOPATH", "LANG"}, "true", true)
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"-o\x00BatchMode=yes",
		"-o\x00StrictHostKeyChecking=accept-new",
		"-o\x00ConnectTimeout=10",
		"-o\x00ForwardAgent=no",
		"-o\x00ClearAllForwardings=yes",
		"-tt",
		"-o\x00SendEnv=GOPATH",
		"-o\x00SendEnv=LANG",
		"-o\x00ProxyJump=bastion",
		"-p\x002222",
		"-i\x00/keys/id_ed25519",
		"worker@sandbox.internal\x00true",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q:\n%v", want, args)
		}
	}
	if args[len(args)-1] != "true" {
		t.Fatalf("last ssh arg = %q, want remote command", args[len(args)-1])
	}

	noTTY := buildSSHArgs(config.SSHIsolationConfig{Host: "host"}, nil, "true", false)
	if !slices.Contains(noTTY, "RequestTTY=no") {
		t.Fatalf("non-tty ssh args missing RequestTTY=no: %v", noTTY)
	}
	if slices.Contains(noTTY, "-tt") {
		t.Fatalf("non-tty ssh args must not force a tty: %v", noTTY)
	}
}

func TestSSHControlSocketPathDependsOnForwardEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh multiplexing is disabled on windows")
	}
	a := sshControlSocketPath(config.SSHIsolationConfig{
		Host:       "sandbox.internal",
		ForwardEnv: []string{"GOPATH"},
	})
	b := sshControlSocketPath(config.SSHIsolationConfig{
		Host:       "sandbox.internal",
		ForwardEnv: []string{"CARGO_HOME"},
	})
	if a == "" || b == "" {
		t.Skip("control socket directory is unavailable")
	}
	if a == b {
		t.Fatalf("control socket path does not depend on SendEnv names: %q", a)
	}
	if len(a) > sshControlPathMaxBytes {
		t.Fatalf("control socket path is too long: %q", a)
	}
}

func TestSSHForwardEnvNames(t *testing.T) {
	names := sshForwardEnvNames(config.SSHIsolationConfig{
		ForwardEnv: []string{"GOPATH", "GOPATH", "LANG", ""},
	})
	if !slices.Contains(names, "LC_*") || !slices.Contains(names, "GOPATH") {
		t.Fatalf("sshForwardEnvNames() = %v, want LC_* and GOPATH", names)
	}
	if slices.Contains(names, "") {
		t.Fatalf("sshForwardEnvNames() = %v, want no empty names", names)
	}
	if len(names) != len(uniqueStrings(names)) {
		t.Fatalf("sshForwardEnvNames() = %v, want deduplicated names", names)
	}
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func TestEnsureSSHClientEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	t.Setenv("HOME", "/real/home")
	t.Setenv("PATH", "/real/bin")
	t.Setenv("SSH_AUTH_SOCK", "/real/agent.sock")

	got := ensureSSHClientEnv([]string{
		"HOME=/instance/home",
		"PATH=/instance/bin",
		"SSH_AUTH_SOCK=/instance/agent.sock",
		"LANG=C",
	})
	joined := strings.Join(got, "\x00")
	if !strings.Contains(joined, "HOME=/real/home") {
		t.Fatalf("HOME was not restored: %v", got)
	}
	if !strings.Contains(joined, "PATH=/instance/bin") {
		t.Fatalf("existing PATH was replaced: %v", got)
	}
	if !strings.Contains(joined, "SSH_AUTH_SOCK=/instance/agent.sock") {
		t.Fatalf("existing SSH_AUTH_SOCK was replaced: %v", got)
	}
	if ensureSSHClientEnv(nil) != nil {
		t.Fatal("nil env should inherit the process environment")
	}

	got = ensureSSHClientEnv([]string{"LANG=C"})
	joined = strings.Join(got, "\x00")
	for _, want := range []string{"PATH=/real/bin", "SSH_AUTH_SOCK=/real/agent.sock"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ensureSSHClientEnv() missing %q: %v", want, got)
		}
	}
}

func TestApplySSHIsolationRewritesCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	binDir := t.TempDir()
	fakeSSH := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in ssh) echo 'unexpected program name in argv' >&2; exit 2;; esac\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", "/real/home")

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "echo hello")
	cmd.Dir = workspace
	cmd.Env = []string{
		"HOME=" + filepath.Join(root, "runtime-user-env", "home"),
		"LANG=C",
		"PATH=" + os.Getenv("PATH"),
		"PICOCLAW_HOME=" + root,
	}
	cfg := config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendSSH,
		SSH: config.SSHIsolationConfig{
			Host:       "sandbox.internal",
			User:       "worker",
			RemoteRoot: "/srv/picoclaw",
		},
	}
	if err := applySSHIsolation(cmd, cfg, root, workspace); err != nil {
		t.Fatalf("applySSHIsolation() error = %v", err)
	}
	if cmd.Path != fakeSSH {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, fakeSSH)
	}
	if cmd.Dir != "" {
		t.Fatalf("cmd.Dir = %q, want empty after ssh rewrite", cmd.Dir)
	}
	joined := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{
		"worker@sandbox.internal",
		"/srv/picoclaw/workspace",
		"echo hello",
		"SendEnv=LC_*",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q: %v", want, cmd.Args)
		}
	}
	for _, unwanted := range []string{"SendEnv=HOME", "SendEnv=PATH", "SendEnv=PICOCLAW_HOME"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("ssh args must not forward %q: %v", unwanted, cmd.Args)
		}
	}
	if !slices.Contains(cmd.Env, "HOME=/real/home") {
		t.Fatalf("cmd.Env HOME was not restored: %v", cmd.Env)
	}
}

func TestPreflightSSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	binDir := t.TempDir()
	fakeSSH := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in ssh) echo 'unexpected program name in argv' >&2; exit 2;; esac\n" +
		"found=0\n" +
		"for arg do case \"$arg\" in SendEnv=LC_\\*) found=1;; esac; done\n" +
		"[ \"$found\" = 1 ] || { echo 'probe is missing SendEnv names' >&2; exit 2; }\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.SSHIsolationConfig{Host: "sandbox.internal"}
	if err := preflightSSH(cfg, "", nil); err != nil {
		t.Fatalf("preflightSSH() error = %v", err)
	}
	if err := preflightSSH(config.SSHIsolationConfig{}, "", nil); err == nil {
		t.Fatal("preflightSSH() should reject a missing host")
	}
	if err := preflightSSH(cfg, "", []config.ExposePath{{Source: "/tmp", Mode: "ro"}}); err == nil {
		t.Fatal("preflightSSH() should reject expose_paths")
	}
}

func TestSSHRunWithFakeClient(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	binDir := t.TempDir()
	fakeSSH := filepath.Join(binDir, "ssh")
	script := "#!/bin/sh\n" +
		"for arg do last=\"$arg\"; done\n" +
		"exec /bin/sh -c \"$last\"\n"
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	t.Setenv(config.EnvHome, root)
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Isolation = config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendSSH,
		SSH: config.SSHIsolationConfig{
			Host:       "fake-host",
			RemoteRoot: root,
		},
	}
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	cmd := exec.Command("/bin/sh", "-c", "echo fake-ssh-ok; pwd")
	cmd.Dir = workspace
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := Run(cmd); err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "fake-ssh-ok") {
		t.Fatalf("output missing marker: %q", output.String())
	}
	if !strings.Contains(output.String(), workspace) {
		t.Fatalf("output missing remote workspace %q: %q", workspace, output.String())
	}
}

func TestDescribeBoundarySSH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := filepath.Join(home, "workspace")

	boundary, ok := DescribeBoundary(workspace, config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendSSH,
		SSH: config.SSHIsolationConfig{
			Host:       "sandbox.internal",
			User:       "worker",
			RemoteRoot: "/srv/picoclaw",
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if boundary.Backend != "SSH" {
		t.Fatalf("Backend = %q, want SSH", boundary.Backend)
	}
	if boundary.PathBoundary {
		t.Fatal("PathBoundary = true, want false for ssh")
	}
	if boundary.Remote != "worker@sandbox.internal" {
		t.Fatalf("Remote = %q, want worker@sandbox.internal", boundary.Remote)
	}
	if boundary.RemoteRoot != "/srv/picoclaw" {
		t.Fatalf("RemoteRoot = %q, want /srv/picoclaw", boundary.RemoteRoot)
	}
	if boundary.RemoteWorkspace != filepath.Join("/srv/picoclaw", "workspace") {
		t.Fatalf("RemoteWorkspace = %q, want /srv/picoclaw/workspace", boundary.RemoteWorkspace)
	}
	if boundary.Workspace != filepath.Clean(workspace) {
		t.Fatalf("Workspace = %q, want local workspace", boundary.Workspace)
	}
	if !boundary.Network {
		t.Fatal("Network = false, want true")
	}
}
