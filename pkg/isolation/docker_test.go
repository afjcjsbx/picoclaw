package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestResolvedBackend(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.IsolationConfig
		want string
	}{
		{
			name: "empty defaults to auto",
			cfg:  config.IsolationConfig{},
			want: nativeBackendForTest(),
		},
		{
			name: "explicit docker",
			cfg:  config.IsolationConfig{Backend: "docker"},
			want: config.IsolationBackendDocker,
		},
		{
			name: "docker case and spaces",
			cfg:  config.IsolationConfig{Backend: " Docker "},
			want: config.IsolationBackendDocker,
		},
		{
			name: "bwrap alias",
			cfg:  config.IsolationConfig{Backend: "bwrap"},
			want: config.IsolationBackendBubblewrap,
		},
		{
			name: "sandbox-exec alias",
			cfg:  config.IsolationConfig{Backend: "sandbox-exec"},
			want: config.IsolationBackendSeatbelt,
		},
		{
			name: "unknown is preserved",
			cfg:  config.IsolationConfig{Backend: "Podman"},
			want: "podman",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvedBackend(tt.cfg); got != tt.want {
				t.Fatalf("resolvedBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func nativeBackendForTest() string {
	switch runtime.GOOS {
	case "linux":
		return config.IsolationBackendBubblewrap
	case "darwin":
		return config.IsolationBackendSeatbelt
	case "windows":
		return config.IsolationBackendWindows
	default:
		return ""
	}
}

func TestValidateBackend(t *testing.T) {
	docker := config.IsolationConfig{Enabled: true, Backend: config.IsolationBackendDocker}
	if err := validateBackend(docker); err != nil {
		t.Fatalf("validateBackend(docker) = %v", err)
	}
	if err := validateBackend(config.IsolationConfig{Enabled: true, Backend: "podman"}); err == nil {
		t.Fatal("validateBackend(podman) = nil, want unsupported backend error")
	}
}

func TestIsBackendSupportedOn(t *testing.T) {
	tests := []struct {
		goos    string
		backend string
		want    bool
	}{
		{goos: "linux", backend: config.IsolationBackendBubblewrap, want: true},
		{goos: "darwin", backend: config.IsolationBackendBubblewrap, want: false},
		{goos: "darwin", backend: config.IsolationBackendSeatbelt, want: true},
		{goos: "windows", backend: config.IsolationBackendWindows, want: true},
		{goos: "freebsd", backend: config.IsolationBackendDocker, want: true},
		{goos: "freebsd", backend: config.IsolationBackendBubblewrap, want: false},
		{goos: "linux", backend: "podman", want: false},
	}
	for _, tt := range tests {
		if got := isBackendSupportedOn(tt.goos, tt.backend); got != tt.want {
			t.Fatalf(
				"isBackendSupportedOn(%q, %q) = %v, want %v",
				tt.goos,
				tt.backend,
				got,
				tt.want,
			)
		}
	}
}

func TestBuildDockerMountPlan(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := BuildDockerMountPlan(root, []config.ExposePath{
		{Source: shared, Target: shared, Mode: "ro"},
	})
	if len(plan) != 2 {
		t.Fatalf("BuildDockerMountPlan() len = %d, want 2", len(plan))
	}
	if plan[0].Source != root || plan[0].Mode != "rw" {
		t.Fatalf("root rule = %+v, want source=%q mode=rw", plan[0], root)
	}
	if plan[1].Source != shared || plan[1].Mode != "ro" {
		t.Fatalf("override rule = %+v, want source=%q mode=ro", plan[1], shared)
	}
}

func TestBuildDockerRunArgs(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := []MountRule{
		{Source: root, Target: root, Mode: "rw"},
		{Source: workspace, Target: workspace, Mode: "ro"},
	}
	cfg := config.DockerIsolationConfig{
		Image:   "alpine:3.20",
		Network: "none",
		User:    "1234:5678",
		Pull:    "never",
	}
	args := buildDockerRunArgs(
		"/bin/sh",
		[]string{"sh", "-c", "echo hello"},
		[]string{
			"HOME=/tmp/home",
			"PATH=/usr/bin",
			"EMPTY=",
			"DOCKER_HOST=unix:///tmp/docker.sock",
		},
		cfg,
		"picoclaw-test",
		workspace,
		plan,
		false,
	)
	joined := strings.Join(args, "\x00")
	for _, want := range []string{
		"run", "--rm", "--init", "--interactive", "--name\x00picoclaw-test",
		"--user\x001234:5678", "--network\x00none", "--pull\x00never",
		"-e\x00HOME", "-e\x00EMPTY",
		"--mount\x00type=bind,source=" + root + ",target=" + root,
		"--mount\x00type=bind,source=" + workspace + ",target=" + workspace + ",readonly",
		"--workdir\x00" + workspace,
		"--entrypoint\x00/bin/sh", "alpine:3.20", "-c\x00echo hello",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q:\n%v", want, args)
		}
	}
	if slices.Contains(args, "--privileged") {
		t.Fatalf("docker args unexpectedly contain --privileged: %v", args)
	}
	if strings.Contains(joined, "-e\x00DOCKER_HOST") {
		t.Fatalf("DOCKER_HOST must stay on the docker CLI, not in the container: %v", args)
	}
	if strings.Contains(joined, "-e\x00PATH") {
		t.Fatalf("host PATH must not replace the image PATH: %v", args)
	}
}

func TestEnsureDockerCLIEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	t.Setenv("DOCKER_CONFIG", "/tmp/docker-config")
	env := ensureDockerCLIEnv([]string{"HOME=/instance/home"})
	joined := strings.Join(env, "\x00")
	for _, want := range []string{
		"HOME=/instance/home",
		"DOCKER_HOST=unix:///tmp/docker.sock",
		"DOCKER_CONFIG=/tmp/docker-config",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ensureDockerCLIEnv() = %v, want %q", env, want)
		}
	}
}

func TestDockerEntrypoint(t *testing.T) {
	tests := map[string]string{
		"/bin/sh":              "/bin/sh",
		"/usr/bin/bash":        "/bin/bash",
		"/workspace/run.sh":    "/workspace/run.sh",
		"./sh":                 "./sh",
		"/usr/local/bin/mycmd": "/usr/local/bin/mycmd",
	}
	for input, want := range tests {
		if got := dockerEntrypoint(input); got != want {
			t.Errorf("dockerEntrypoint(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildDockerRunArgsUsesDefaultNetwork(t *testing.T) {
	cfg := config.DockerIsolationConfig{Image: "alpine:3.20"}
	args := buildDockerRunArgs(
		"/bin/sh",
		[]string{"sh", "-c", "true"},
		nil,
		cfg,
		"name",
		"",
		nil,
		false,
	)
	if slices.Contains(args, "--network") {
		t.Fatalf("docker args should omit --network so Docker's default (enabled) applies: %v", args)
	}
	if !cfg.NetworkEnabled() {
		t.Fatal("default Docker network should be enabled")
	}
	if got := cfg.EffectivePull(); got != "missing" {
		t.Fatalf("EffectivePull() = %q, want missing", got)
	}
}

func TestValidateDockerConfig(t *testing.T) {
	valid := config.DockerIsolationConfig{Image: "alpine:3.20"}
	if err := validateDockerConfig(valid); err != nil {
		t.Fatalf("validateDockerConfig(valid) = %v", err)
	}

	tests := []struct {
		name string
		cfg  config.DockerIsolationConfig
	}{
		{name: "missing image", cfg: config.DockerIsolationConfig{}},
		{name: "image starts with dash", cfg: config.DockerIsolationConfig{Image: "-alpine"}},
		{name: "image with space", cfg: config.DockerIsolationConfig{Image: "alpine 3.20"}},
		{name: "bad pull", cfg: config.DockerIsolationConfig{Image: "alpine", Pull: "sometimes"}},
		{
			name: "network starts with dash",
			cfg:  config.DockerIsolationConfig{Image: "alpine", Network: "-host"},
		},
		{name: "user with space", cfg: config.DockerIsolationConfig{Image: "alpine", User: "1 2"}},
		{
			name: "empty extra arg",
			cfg:  config.DockerIsolationConfig{Image: "alpine", ExtraArgs: []string{""}},
		},
		{
			name: "detach",
			cfg:  config.DockerIsolationConfig{Image: "alpine", ExtraArgs: []string{"--detach"}},
		},
		{
			name: "name override",
			cfg: config.DockerIsolationConfig{
				Image:     "alpine",
				ExtraArgs: []string{"--name=other"},
			},
		},
		{
			name: "entrypoint override",
			cfg: config.DockerIsolationConfig{
				Image:     "alpine",
				ExtraArgs: []string{"--entrypoint", "/bin/bash"},
			},
		},
		{
			name: "cidfile",
			cfg: config.DockerIsolationConfig{
				Image:     "alpine",
				ExtraArgs: []string{"--cidfile=/tmp/x"},
			},
		},
		{
			name: "rm override",
			cfg:  config.DockerIsolationConfig{Image: "alpine", ExtraArgs: []string{"--rm=false"}},
		},
		{
			name: "end of options",
			cfg:  config.DockerIsolationConfig{Image: "alpine", ExtraArgs: []string{"--"}},
		},
		{
			name: "combined detach",
			cfg:  config.DockerIsolationConfig{Image: "alpine", ExtraArgs: []string{"-dt"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDockerConfig(tt.cfg); err == nil {
				t.Fatal("validateDockerConfig() = nil, want error")
			}
		})
	}
}

func TestAppendDockerArgumentMounts(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "out", "result.txt")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := appendDockerArgumentMounts(nil, []string{
		input,
		"--output=" + output,
		string(filepath.Separator),
		"/etc/hosts",
	})
	foundInput, foundOutput := false, false
	for _, rule := range plan {
		if rule.Source == input && rule.Mode == "ro" {
			foundInput = true
		}
		if rule.Source == filepath.Dir(output) && rule.Mode == "rw" {
			foundOutput = true
		}
	}
	if !foundInput || !foundOutput {
		t.Fatalf("appendDockerArgumentMounts() = %+v, want input=%q ro and output parent=%q rw",
			plan, input, filepath.Dir(output))
	}
	for _, rule := range plan {
		if dockerSystemPath(rule.Source) || dockerFilesystemRoot(rule.Source) {
			t.Fatalf("plan unexpectedly exposes host system path: %+v", rule)
		}
	}
}

func TestValidateDockerMountPlan(t *testing.T) {
	root := t.TempDir()
	if err := validateDockerMountPlan([]MountRule{{Source: root, Target: root, Mode: "rw"}}); err != nil {
		t.Fatalf("validateDockerMountPlan(valid) = %v", err)
	}
	if err := validateDockerMountPlan([]MountRule{{Source: "/does/not/exist", Target: "/x", Mode: "rw"}}); err == nil {
		t.Fatal("validateDockerMountPlan() should reject a missing source")
	}
	if err := validateDockerMountPlan([]MountRule{{Source: "/tmp/a,b", Target: "/x", Mode: "rw"}}); err == nil {
		t.Fatal("validateDockerMountPlan() should reject a comma in a mount path")
	}
	if err := validateDockerMountPlan([]MountRule{{Source: root, Target: root, Mode: "bad"}}); err == nil {
		t.Fatal("validateDockerMountPlan() should reject an invalid mode")
	}
	if err := validateDockerMountPlan([]MountRule{{
		Source: root,
		Target: string(filepath.Separator),
		Mode:   "rw",
	}}); err == nil {
		t.Fatal("validateDockerMountPlan() should reject the filesystem root as a target")
	}
}

func TestApplyDockerIsolationRewritesCommand(t *testing.T) {
	binDir := t.TempDir()
	fakeDocker := filepath.Join(binDir, "docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "echo hello")
	cmd.Dir = workspace
	cmd.Env = []string{"HOME=" + filepath.Join(root, "home")}
	cfg := config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendDocker,
		Docker:  config.DockerIsolationConfig{Image: "alpine:3.20", Network: "none"},
	}
	if err := applyDockerIsolation(cmd, cfg, root); err != nil {
		t.Fatalf("applyDockerIsolation() error = %v", err)
	}
	if cmd.Path != fakeDocker {
		t.Fatalf("cmd.Path = %q, want %q", cmd.Path, fakeDocker)
	}
	if cmd.Dir != "" {
		t.Fatalf("cmd.Dir = %q, want empty after docker rewrite", cmd.Dir)
	}
	joined := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{
		"run", "--name", "picoclaw-", "--workdir\x00" + workspace,
		"--entrypoint\x00/bin/sh", "alpine:3.20", "-c\x00echo hello",
		"type=bind,source=" + workspace + ",target=" + workspace,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %v", want, cmd.Args)
		}
	}
	Release(cmd)
}

func TestDescribeBoundaryDockerDefaultNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	boundary, ok := DescribeBoundary(filepath.Join(home, "workspace"), config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendDocker,
		Docker:  config.DockerIsolationConfig{},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if !boundary.Network {
		t.Fatal("Network = false, want true by default")
	}
}

func TestDescribeBoundaryDocker(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := filepath.Join(home, "workspace")
	shared := filepath.Join(t.TempDir(), "shared")

	boundary, ok := DescribeBoundary(workspace, config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendDocker,
		ExposePaths: []config.ExposePath{
			{Source: shared, Target: shared, Mode: "ro"},
		},
		Docker: config.DockerIsolationConfig{Network: "none"},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if boundary.Backend != "Docker" {
		t.Fatalf("Backend = %q, want Docker", boundary.Backend)
	}
	if !boundary.PathBoundary || !boundary.RootWritable || !boundary.ScratchWritable {
		t.Fatalf("unexpected boundary flags: %+v", boundary)
	}
	if boundary.Network {
		t.Fatal("Network = true, want false for network=none")
	}
	if !slices.Contains(boundary.ReadOnly, shared) {
		t.Fatalf("ReadOnly = %v, want %q", boundary.ReadOnly, shared)
	}
}
