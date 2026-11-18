package isolation

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	dockerContainerLabel = "picoclaw.isolation"
	dockerCleanupTimeout = 5 * time.Second
	dockerInfoTimeout    = 15 * time.Second
)

var (
	dockerContainersByCmd sync.Map // *exec.Cmd -> container name
	dockerContainersByPID sync.Map // int -> container name
)

// applyDockerIsolation rewrites the command so it runs inside a Docker
// container. The image provides the command and its runtime dependencies; the
// host instance root, working directory and explicitly exposed paths are bind
// mounted at the same absolute paths so relative and absolute arguments keep
// their meaning.
func applyDockerIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled || cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return nil
	}
	if err := validateDockerConfig(isolation.Docker); err != nil {
		return err
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf(
			"docker isolation requires the docker CLI on PATH: %w; "+
				"install Docker or set isolation.backend to %q",
			err,
			config.IsolationBackendAuto,
		)
	}

	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	execDir, err := resolveDockerWorkingDir(cmd.Dir, originalPath)
	if err != nil {
		return err
	}
	// The Docker CLI may need the host Docker context even though the child
	// environment was redirected to the instance home. These variables are
	// kept for the CLI and are not forwarded into the container.
	cmd.Env = ensureDockerCLIEnv(cmd.Env)

	plan := BuildDockerMountPlan(root, isolation.ExposePaths)
	if execDir != "" {
		plan = ensureDockerMountRule(plan, execDir, execDir, "rw")
	}
	plan = appendDockerCommandMountRules(plan, originalPath, execDir, root)
	plan = appendDockerArgumentMounts(plan, originalArgs[1:])
	if err := validateDockerMountPlan(plan); err != nil {
		return err
	}

	name := "picoclaw-" + uuid.NewString()
	dockerArgs := buildDockerRunArgs(
		dockerCommandEntrypoint(originalPath, execDir),
		originalArgs,
		cmd.Env,
		isolation.Docker,
		name,
		execDir,
		plan,
		dockerIsTerminal(cmd.Stdin),
	)
	logger.DebugCF("isolation", "docker isolation container plan", map[string]any{
		"root":          root,
		"image":         isolation.Docker.Image,
		"container":     name,
		"command":       originalPath,
		"working_dir":   execDir,
		"network":       isolation.Docker.EffectiveNetwork(),
		"pull":          isolation.Docker.EffectivePull(),
		"mounts":        formatDockerMountPlan(plan),
		"env_forward":   len(cmd.Env),
		"extra_args":    len(isolation.Docker.ExtraArgs),
		"path_boundary": true,
	})

	dockerContainersByCmd.Store(cmd, name)
	cmd.Path = dockerPath
	cmd.Args = dockerArgs
	cmd.Dir = ""
	return nil
}

func postStartDockerIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled || cmd == nil || cmd.Process == nil {
		return nil
	}
	nameAny, ok := dockerContainersByCmd.Load(cmd)
	if !ok {
		return nil
	}
	name, _ := nameAny.(string)
	if name == "" {
		return nil
	}
	dockerContainersByPID.Store(cmd.Process.Pid, name)
	logger.DebugCF("isolation", "docker isolation container started", map[string]any{
		"container": name,
		"pid":       cmd.Process.Pid,
		"root":      root,
	})
	return nil
}

func cleanupPendingDockerResources(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	nameAny, ok := dockerContainersByCmd.LoadAndDelete(cmd)
	if !ok {
		return
	}
	name, _ := nameAny.(string)
	if cmd.Process != nil {
		dockerContainersByPID.Delete(cmd.Process.Pid)
	}
	cleanupDockerContainer(name)
}

// CleanupPID removes the Docker container associated with a child process.
// Callers use it when they kill a process group, because killing the docker CLI
// does not stop the container it started.
func CleanupPID(pid int) {
	if pid <= 0 {
		return
	}
	nameAny, ok := dockerContainersByPID.LoadAndDelete(pid)
	if !ok {
		return
	}
	name, _ := nameAny.(string)
	cleanupDockerContainer(name)
}

// Release drops the bookkeeping associated with a command after it exits
// normally. The container itself is already removed by docker run --rm.
func Release(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	nameAny, ok := dockerContainersByCmd.LoadAndDelete(cmd)
	if !ok || cmd.Process == nil {
		return
	}
	name, _ := nameAny.(string)
	if current, ok := dockerContainersByPID.Load(cmd.Process.Pid); ok {
		currentName, _ := current.(string)
		if currentName == name {
			dockerContainersByPID.Delete(cmd.Process.Pid)
		}
	}
}

func cleanupDockerContainer(name string) {
	if name == "" {
		return
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerCleanupTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, dockerPath, "rm", "-f", name).Run()
}

func resolveDockerWorkingDir(dir, commandPath string) (string, error) {
	if strings.TrimSpace(dir) != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolve docker working dir %s: %w", dir, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("docker working dir %s: %w", abs, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("docker working dir is not a directory: %s", abs)
		}
		return filepath.Clean(abs), nil
	}
	if !isRelativeDockerCommandPath(commandPath) {
		return "", nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working dir for docker: %w", err)
	}
	return filepath.Clean(wd), nil
}

func isRelativeDockerCommandPath(path string) bool {
	return !filepath.IsAbs(path) && strings.ContainsRune(path, filepath.Separator)
}

func buildDockerRunArgs(
	entrypoint string,
	originalArgs []string,
	env []string,
	docker config.DockerIsolationConfig,
	name string,
	execDir string,
	plan []MountRule,
	tty bool,
) []string {
	args := []string{
		"docker",
		"run",
		"--rm",
		"--init",
		"--interactive",
		"--name", name,
		"--label", dockerContainerLabel + "=true",
	}
	if tty {
		args = append(args, "--tty")
	}
	if user := dockerUserSpec(docker.User); user != "" {
		args = append(args, "--user", user)
	}
	if network := docker.EffectiveNetwork(); network != "" {
		args = append(args, "--network", network)
	}
	args = append(args, "--pull", docker.EffectivePull())

	// docker run -e NAME reads the value from the docker CLI environment. The
	// repository sanitizes cmd.Env before isolation runs, so values are not
	// repeated on the command line.
	for _, item := range env {
		if idx := strings.IndexByte(item, '='); idx > 0 {
			name := item[:idx]
			// PATH describes the host, not the image. Docker uses the image
			// PATH so tools such as Go and Python resolve consistently; a
			// custom PATH can still be set through docker.extra_args.
			if name == "PATH" || strings.HasPrefix(name, "DOCKER_") {
				continue
			}
			args = append(args, "-e", name)
		}
	}
	for _, rule := range plan {
		args = append(args, "--mount", dockerMountSpec(rule))
	}
	if execDir != "" {
		args = append(args, "--workdir", execDir)
	}
	args = append(args, docker.ExtraArgs...)
	args = append(args, "--entrypoint", entrypoint, docker.Image)
	if len(originalArgs) > 1 {
		args = append(args, originalArgs[1:]...)
	}
	return args
}

func dockerCommandEntrypoint(originalPath, execDir string) string {
	if isRelativeDockerCommandPath(originalPath) && execDir != "" {
		return filepath.Join(execDir, originalPath)
	}
	return dockerEntrypoint(originalPath)
}

// dockerEntrypoint maps host shell paths such as /usr/bin/sh to the shell name
// so the image can resolve its own /bin/sh. Other commands keep their path
// because they may live inside a mounted workspace.
func dockerEntrypoint(originalPath string) string {
	if !filepath.IsAbs(originalPath) || !dockerSystemPath(originalPath) {
		return originalPath
	}
	switch base := filepath.Base(originalPath); base {
	case "sh", "bash", "dash", "zsh", "ksh":
		return "/bin/" + base
	case "fish":
		return "/usr/bin/fish"
	default:
		return originalPath
	}
}

func dockerIsTerminal(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

// ensureDockerCLIEnv supplies the Docker connection settings that the Docker
// CLI needs after ApplyUserEnv redirects HOME. The container never receives
// these names; they are filtered in buildDockerRunArgs.
func ensureDockerCLIEnv(env []string) []string {
	present := make(map[string]bool, len(env))
	for _, item := range env {
		if idx := strings.IndexByte(item, '='); idx > 0 {
			present[item[:idx]] = true
		}
	}
	out := append([]string(nil), env...)
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		if present[name] {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+value)
			present[name] = true
		}
	}
	if present["DOCKER_CONFIG"] {
		return out
	}
	if value, ok := os.LookupEnv("DOCKER_CONFIG"); ok {
		return append(out, "DOCKER_CONFIG="+value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	candidate := filepath.Join(home, ".docker")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		out = append(out, "DOCKER_CONFIG="+candidate)
	}
	return out
}

func dockerMountSpec(rule MountRule) string {
	spec := fmt.Sprintf("type=bind,source=%s,target=%s", rule.Source, rule.Target)
	if rule.Mode != "rw" {
		spec += ",readonly"
	}
	return spec
}

// BuildDockerMountPlan converts the merged expose-path configuration into the
// bind mounts used by the Docker backend. The instance root is always mounted
// read-write; system paths are not copied from the host because the image
// provides its own userland.
func BuildDockerMountPlan(root string, overrides []config.ExposePath) []MountRule {
	merged := MergeExposePaths(defaultDockerExposePaths(root), overrides)
	plan := make([]MountRule, 0, len(merged))
	for _, item := range merged {
		plan = append(plan, MountRule{Source: item.Source, Target: item.Target, Mode: item.Mode})
	}
	return plan
}

func defaultDockerExposePaths(root string) []config.ExposePath {
	return []config.ExposePath{{Source: root, Target: root, Mode: "rw"}}
}

func ensureDockerMountRule(plan []MountRule, source, target, mode string) []MountRule {
	cleanSource := filepath.Clean(source)
	cleanTarget := filepath.Clean(target)
	for _, rule := range plan {
		if filepath.Clean(rule.Target) == cleanTarget {
			return plan
		}
	}
	return append(plan, MountRule{Source: cleanSource, Target: cleanTarget, Mode: mode})
}

// appendDockerCommandMountRules exposes a command that lives outside the
// instance root and the working directory. System paths are skipped so the
// image's own binaries are not shadowed by host files.
func appendDockerCommandMountRules(
	plan []MountRule,
	commandPath, execDir, root string,
) []MountRule {
	if commandPath == "" || dockerSystemPath(commandPath) {
		return plan
	}
	path := commandPath
	if !filepath.IsAbs(path) {
		if execDir == "" {
			return plan
		}
		path = filepath.Join(execDir, path)
	}
	path = filepath.Clean(path)
	if boundaryPathWithin(root, path) || (execDir != "" && boundaryPathWithin(execDir, path)) {
		return plan
	}
	info, err := os.Stat(path)
	if err != nil {
		return plan
	}
	if info.IsDir() {
		return plan
	}
	plan = ensureDockerMountRule(plan, path, path, "ro")
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		plan = ensureDockerMountRule(plan, resolved, resolved, "ro")
	}
	if parent := filepath.Dir(path); parent != path && !dockerFilesystemRoot(parent) {
		plan = ensureDockerMountRule(plan, parent, parent, "ro")
	}
	return plan
}

func appendDockerArgumentMounts(plan []MountRule, args []string) []MountRule {
	for _, arg := range args {
		path, ok := dockerArgumentPath(arg)
		if !ok {
			continue
		}
		clean := filepath.Clean(path)
		// Host system paths are deliberately left to the image. Mounting the
		// host root (or /usr) over the container root would replace the image
		// userland and can break the command before it starts.
		if dockerFilesystemRoot(clean) || dockerSystemPath(clean) {
			continue
		}
		if _, err := os.Stat(clean); err == nil {
			plan = ensureDockerMountRule(plan, clean, clean, "ro")
			if resolved, resolveErr := filepath.EvalSymlinks(clean); resolveErr == nil &&
				resolved != clean {
				plan = ensureDockerMountRule(plan, resolved, resolved, "ro")
			}
			continue
		} else if !os.IsNotExist(err) {
			continue
		}
		parent := filepath.Dir(clean)
		if parent == clean || dockerFilesystemRoot(parent) || dockerSystemPath(parent) {
			continue
		}
		if _, err := os.Stat(parent); err == nil {
			plan = ensureDockerMountRule(plan, parent, parent, "rw")
		}
	}
	return plan
}

func dockerFilesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) {
		return true
	}
	volume := filepath.VolumeName(clean)
	return volume != "" && clean == volume+string(filepath.Separator)
}

func dockerArgumentPath(arg string) (string, bool) {
	if filepath.IsAbs(arg) {
		return arg, true
	}
	idx := strings.IndexRune(arg, '=')
	if idx <= 0 || idx == len(arg)-1 {
		return "", false
	}
	value := arg[idx+1:]
	if !filepath.IsAbs(value) {
		return "", false
	}
	return value, true
}

func dockerSystemPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, prefix := range []string{
		"/bin", "/sbin", "/usr", "/lib", "/lib64", "/etc", "/System", "/Library/Apple",
	} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	// Windows containers use these roots for the image userland.
	return strings.HasPrefix(strings.ToLower(clean), "c:/windows")
}

func validateDockerConfig(cfg config.DockerIsolationConfig) error {
	if strings.TrimSpace(cfg.Image) == "" {
		return fmt.Errorf(
			"isolation.docker.image is required when isolation.backend is %q",
			config.IsolationBackendDocker,
		)
	}
	if err := validateDockerToken("image", cfg.Image); err != nil {
		return err
	}
	if network := cfg.EffectiveNetwork(); network != "" {
		if err := validateDockerToken("network", network); err != nil {
			return err
		}
	}
	if user := strings.TrimSpace(cfg.User); user != "" {
		if err := validateDockerToken("user", user); err != nil {
			return err
		}
	}
	switch cfg.EffectivePull() {
	case "always", "missing", "never":
	default:
		return fmt.Errorf(
			"isolation.docker.pull must be always, missing or never, got %q",
			cfg.Pull,
		)
	}
	for i, arg := range cfg.ExtraArgs {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("isolation.docker.extra_args[%d] must not be empty", i)
		}
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("isolation.docker.extra_args[%d] contains a NUL byte", i)
		}
		if dockerLifecycleFlag(arg) {
			return fmt.Errorf(
				"isolation.docker.extra_args[%d] must not override container lifecycle: %q",
				i,
				arg,
			)
		}
	}
	return nil
}

// dockerLifecycleFlag rejects extra arguments that would break the exec-style
// lifecycle or the bookkeeping used to remove a container when a command is
// killed.
func dockerLifecycleFlag(arg string) bool {
	name := arg
	if idx := strings.IndexByte(name, '='); idx >= 0 {
		name = name[:idx]
	}
	switch name {
	case "-d", "--detach", "--name", "--cidfile", "--entrypoint",
		"--rm", "--interactive", "-i", "--":
		return true
	}
	// Reject combined short flags such as -dt or -itd that contain detach.
	if strings.HasPrefix(name, "-") && !strings.HasPrefix(name, "--") &&
		strings.ContainsRune(name[1:], 'd') {
		return true
	}
	return false
}

func validateDockerToken(field, value string) error {
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("isolation.docker.%s must not start with '-': %q", field, value)
	}
	if strings.ContainsAny(value, " \t\r\n\x00") {
		return fmt.Errorf(
			"isolation.docker.%s must not contain whitespace or NUL bytes: %q",
			field,
			value,
		)
	}
	return nil
}

func validateDockerMountPlan(plan []MountRule) error {
	for _, rule := range plan {
		if rule.Mode != "ro" && rule.Mode != "rw" {
			return fmt.Errorf("invalid docker mount mode for %s: %s", rule.Target, rule.Mode)
		}
		if !filepath.IsAbs(rule.Source) || !filepath.IsAbs(rule.Target) {
			return fmt.Errorf(
				"docker mount source and target must be absolute: %s -> %s",
				rule.Source,
				rule.Target,
			)
		}
		if strings.ContainsAny(rule.Source, ",\x00\r\n") ||
			strings.ContainsAny(rule.Target, ",\x00\r\n") {
			return fmt.Errorf(
				"docker bind mount paths must not contain ',', NUL or newline characters: %s -> %s",
				rule.Source,
				rule.Target,
			)
		}
		if dockerFilesystemRoot(rule.Target) {
			return fmt.Errorf(
				"docker mount target must not be the filesystem root: %s",
				rule.Target,
			)
		}
		if _, err := os.Stat(rule.Source); err != nil {
			return fmt.Errorf("docker mount source %s: %w", rule.Source, err)
		}
	}
	return nil
}

func preflightDocker(
	cfg config.DockerIsolationConfig,
	root string,
	expose []config.ExposePath,
) error {
	if err := validateDockerConfig(cfg); err != nil {
		return err
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker isolation requires the docker CLI on PATH: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dockerInfoTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, dockerPath, "info", "--format", "{{.ServerVersion}}").
		CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"docker isolation requires a reachable Docker daemon: %w: %s",
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return validateDockerMountPlan(BuildDockerMountPlan(root, expose))
}

func formatDockerMountPlan(plan []MountRule) []map[string]string {
	formatted := make([]map[string]string, 0, len(plan))
	for _, rule := range plan {
		formatted = append(formatted, map[string]string{
			"source": rule.Source,
			"target": rule.Target,
			"mode":   rule.Mode,
		})
	}
	return formatted
}
