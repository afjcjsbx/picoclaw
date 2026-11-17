package isolation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	sshControlPersistSeconds = 60
	sshConnectTimeoutSeconds = 10
	sshPreflightTimeout      = 20 * time.Second
	// ControlPath is a Unix socket. Leave room for OpenSSH's temporary
	// listener suffix inside the platform sun_path limit (104 bytes on macOS).
	sshControlPathMaxBytes = 80
)

var (
	sshControlDirOnce sync.Once
	sshControlDirPath string
)

// applySSHIsolation rewrites the command so it runs on a remote host through
// the OpenSSH client. The remote account is the isolation boundary: local host
// paths are not mounted, and the remote host must provide the command, /bin/sh
// for the exec tool, and the mapped working directory.
func applySSHIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled || cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf(
			"ssh isolation backend is not supported on windows; use docker or the native backend",
		)
	}
	if err := validateSSHIsolation(isolation); err != nil {
		return err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf(
			"ssh isolation requires the OpenSSH client on PATH: %w; install openssh-client or set isolation.backend to %q",
			err,
			config.IsolationBackendAuto,
		)
	}

	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	execDir, err := resolveSSHWorkingDir(cmd.Dir, originalPath)
	if err != nil {
		return err
	}
	remoteRoot := sshRemoteRoot(root, isolation.SSH.RemoteRoot)
	remoteDir := sshRemotePath(execDir, root, isolation.SSH.RemoteRoot)
	remoteCommandPath := sshRemoteCommandPath(originalPath, remoteDir, root, isolation.SSH.RemoteRoot)
	remoteCommand := buildSSHRemoteCommand(remoteDir, remoteCommandPath, originalArgs[1:])
	forwardEnv := sshForwardEnvNames(isolation.SSH)

	// Restore the client's real HOME and agent socket: ApplyUserEnv redirected
	// them to the local instance before the backend ran, but ssh needs the
	// operator's keys, known_hosts and ssh-agent. Remote HOME/PATH keep the
	// remote login shell defaults because those names are not forwarded.
	cmd.Env = ensureSSHClientEnv(cmd.Env)

	multiplex := sshMultiplexArgs(isolation.SSH)
	logger.DebugCF("isolation", "ssh isolation remote plan", map[string]any{
		"target":        isolation.SSH.Target(),
		"host":          isolation.SSH.Host,
		"remote_root":   remoteRoot,
		"working_dir":   remoteDir,
		"command":       remoteCommandPath,
		"forward_env":   len(forwardEnv),
		"multiplex":     len(multiplex) > 0,
		"path_boundary": false,
	})

	cmd.Path = sshPath
	cmd.Args = buildSSHArgs(isolation.SSH, forwardEnv, remoteCommand, isTerminalInput(cmd.Stdin))
	cmd.Dir = ""
	return nil
}

func postStartSSHIsolation(
	_ *exec.Cmd,
	_ config.IsolationConfig,
	_ string,
) error {
	return nil
}

func cleanupPendingSSHResources(_ *exec.Cmd) {}

// buildSSHArgs assembles one ssh invocation. Environment values are never
// repeated on the command line: SendEnv carries names only and the ssh client
// reads the values from cmd.Env.
func buildSSHArgs(
	cfg config.SSHIsolationConfig,
	forwardEnv []string,
	remoteCommand string,
	tty bool,
) []string {
	args := []string{"ssh"}
	args = append(args, sshMultiplexArgs(cfg)...)
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout="+strconv.Itoa(sshConnectTimeoutSeconds),
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		// Keep the remote account inside its own host: do not grant access to
		// the local agent or X11, forward ports, or run local commands from
		// ssh_config.
		"-o", "ForwardAgent=no",
		"-o", "ForwardX11=no",
		"-o", "ForwardX11Trusted=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "PermitLocalCommand=no",
	)
	if tty {
		args = append(args, "-tt")
	} else {
		args = append(args, "-o", "RequestTTY=no")
	}
	for _, name := range forwardEnv {
		args = append(args, "-o", "SendEnv="+name)
	}
	args = append(args, cfg.ExtraArgs...)
	if port := cfg.EffectivePort(); port != 22 {
		args = append(args, "-p", strconv.Itoa(port))
	}
	if key := strings.TrimSpace(fileutil.ExpandHome(cfg.KeyPath)); key != "" {
		args = append(args, "-i", key)
	}
	return append(args, cfg.Target(), remoteCommand)
}

func sshMultiplexArgs(cfg config.SSHIsolationConfig) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	path := sshControlSocketPath(cfg)
	if path == "" {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + path,
		"-o", "ControlPersist=" + strconv.Itoa(sshControlPersistSeconds),
	}
}

// sshControlSocketPath keeps the socket short enough for sun_path and uses a
// deterministic per-target name so repeated child processes reuse one master.
func sshControlSocketPath(cfg config.SSHIsolationConfig) string {
	dir := sshControlDir()
	if dir == "" {
		return ""
	}
	key := cfg.Target() + "\x00" + strconv.Itoa(cfg.EffectivePort()) + "\x00" + cfg.KeyPath +
		"\x00" + strings.Join(sshForwardEnvNames(cfg), "\x00") +
		"\x00" + strings.Join(cfg.ExtraArgs, "\x00")
	sum := sha256.Sum256([]byte(key))
	path := filepath.Join(dir, fmt.Sprintf("%x.sock", sum[:8]))
	if len(path) > sshControlPathMaxBytes {
		return ""
	}
	return path
}

// sshControlDir returns a private directory for OpenSSH control sockets. The
// directory is shared by the user's PicoClaw processes so a connection can be
// reused across commands; ControlPersist closes it after the idle period.
func sshControlDir() string {
	sshControlDirOnce.Do(func() {
		root := "/tmp"
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			root = os.TempDir()
		}
		dir := filepath.Join(root, "picoclaw-ssh-"+strconv.Itoa(os.Getuid()))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return
		}
		sshControlDirPath = dir
	})
	return sshControlDirPath
}

func ensureSSHClientEnv(env []string) []string {
	if env == nil {
		return nil
	}
	out := append([]string(nil), env...)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = replaceEnvValue(out, "HOME", home)
		if runtime.GOOS == "windows" {
			out = replaceEnvValue(out, "USERPROFILE", home)
		}
	}
	for _, name := range []string{"SSH_AUTH_SOCK", "SSH_AGENT_PID", "PATH"} {
		if value, ok := os.LookupEnv(name); ok {
			out = ensureEnvValue(out, name, value)
		}
	}
	return out
}

func replaceEnvValue(env []string, name, value string) []string {
	prefix := name + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func ensureEnvValue(env []string, name, value string) []string {
	prefix := name + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return env
		}
	}
	return append(env, prefix+value)
}

func resolveSSHWorkingDir(dir, commandPath string) (string, error) {
	if strings.TrimSpace(dir) != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolve ssh working dir %s: %w", dir, err)
		}
		return filepath.Clean(abs), nil
	}
	if !isRelativeSSHCommandPath(commandPath) {
		return "", nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current working dir for ssh: %w", err)
	}
	return filepath.Clean(wd), nil
}

func isRelativeSSHCommandPath(path string) bool {
	return !filepath.IsAbs(path) && strings.ContainsRune(path, filepath.Separator)
}

// sshRemoteRoot returns the remote path that corresponds to the local instance
// root. An empty override means the remote host uses the same absolute path.
func sshRemoteRoot(localRoot, remoteRoot string) string {
	remoteRoot = strings.TrimSpace(remoteRoot)
	if remoteRoot != "" {
		return filepath.Clean(remoteRoot)
	}
	if localRoot == "" {
		return ""
	}
	return filepath.Clean(localRoot)
}

// sshRemotePath maps a local instance-root path to remoteRoot. Paths outside
// the instance root keep their local absolute path; callers document that such
// paths must exist on the remote host as well.
func sshRemotePath(localPath, localRoot, remoteRoot string) string {
	localPath = strings.TrimSpace(localPath)
	if localPath == "" {
		return ""
	}
	localPath = filepath.Clean(localPath)
	remoteRoot = strings.TrimSpace(remoteRoot)
	if remoteRoot == "" || localRoot == "" {
		return localPath
	}
	localRoot = filepath.Clean(localRoot)
	if !boundaryPathWithin(localRoot, localPath) {
		return localPath
	}
	rel, err := filepath.Rel(localRoot, localPath)
	if err != nil {
		return localPath
	}
	if rel == "." {
		return filepath.Clean(remoteRoot)
	}
	return filepath.Clean(filepath.Join(remoteRoot, rel))
}

func sshRemoteCommandPath(originalPath, remoteDir, root, remoteRoot string) string {
	if filepath.IsAbs(originalPath) {
		path := sshPortableShellPath(originalPath, root)
		return sshRemotePath(path, root, remoteRoot)
	}
	if isRelativeSSHCommandPath(originalPath) && remoteDir != "" {
		return filepath.Clean(filepath.Join(remoteDir, originalPath))
	}
	return originalPath
}

// sshPortableShellPath maps a host system sh to /bin/sh. The exec tool only
// needs a POSIX shell, and the remote host may keep sh in a different system
// directory (for example /usr/bin/sh). A workspace script named sh is left
// alone.
func sshPortableShellPath(path, root string) string {
	if filepath.Base(path) != "sh" || boundaryPathWithin(root, path) {
		return path
	}
	switch filepath.Dir(path) {
	case "/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin":
		return "/bin/sh"
	default:
		return path
	}
}

// buildSSHRemoteCommand runs the original argv through /bin/sh on the remote
// host. The extra shell layer keeps quoting predictable even when the remote
// login shell is not POSIX (for example fish or csh).
func buildSSHRemoteCommand(dir, commandPath string, args []string) string {
	var inner strings.Builder
	if dir != "" {
		inner.WriteString("cd ")
		inner.WriteString(sshShellQuote(dir))
		inner.WriteString(" && ")
	}
	inner.WriteString("exec ")
	inner.WriteString(sshShellQuote(commandPath))
	for _, arg := range args {
		inner.WriteByte(' ')
		inner.WriteString(sshShellQuote(arg))
	}
	return "/bin/sh -c " + sshShellQuote(inner.String())
}

// sshShellQuote quotes one argument for a POSIX shell. Safe characters are
// left unquoted so common commands stay readable in logs.
func sshShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("_@%+=:,./-", r) {
			continue
		}
		safe = false
		break
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func sshForwardEnvNames(cfg config.SSHIsolationConfig) []string {
	names := cfg.EffectiveForwardEnv()
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func validateSSHIsolation(isolation config.IsolationConfig) error {
	if err := validateSSHConfig(isolation.SSH); err != nil {
		return err
	}
	if len(isolation.ExposePaths) > 0 {
		return fmt.Errorf(
			"ssh isolation does not support expose_paths: the remote account already sees its own filesystem",
		)
	}
	return nil
}

func validateSSHConfig(cfg config.SSHIsolationConfig) error {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return fmt.Errorf(
			"isolation.ssh.host is required when isolation.backend is %q",
			config.IsolationBackendSSH,
		)
	}
	if strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t\r\n\x00") {
		return fmt.Errorf("isolation.ssh.host is invalid: %q", cfg.Host)
	}
	user := strings.TrimSpace(cfg.User)
	if user != "" {
		if strings.HasPrefix(user, "-") || strings.ContainsAny(user, " \t\r\n\x00@") {
			return fmt.Errorf("isolation.ssh.user is invalid: %q", cfg.User)
		}
		if strings.Contains(host, "@") {
			return fmt.Errorf(
				"isolation.ssh.host must not contain '@' when isolation.ssh.user is set",
			)
		}
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("isolation.ssh.port must be between 1 and 65535, got %d", cfg.Port)
	}
	if key := strings.TrimSpace(cfg.KeyPath); key != "" && strings.ContainsAny(key, "\x00\r\n") {
		return fmt.Errorf("isolation.ssh.key_path must not contain NUL or newline characters")
	}
	if remoteRoot := strings.TrimSpace(cfg.RemoteRoot); remoteRoot != "" {
		if !filepath.IsAbs(remoteRoot) {
			return fmt.Errorf(
				"isolation.ssh.remote_root must be an absolute remote path, got %q",
				cfg.RemoteRoot,
			)
		}
		if strings.ContainsAny(remoteRoot, "\x00\r\n") {
			return fmt.Errorf("isolation.ssh.remote_root must not contain NUL or newline characters")
		}
	}
	for i, name := range cfg.ForwardEnv {
		if err := validateSSHEnvPattern(name); err != nil {
			return fmt.Errorf("isolation.ssh.forward_env[%d]: %w", i, err)
		}
	}
	for i, arg := range cfg.ExtraArgs {
		if err := validateSSHExtraArg(arg); err != nil {
			return fmt.Errorf("isolation.ssh.extra_args[%d]: %w", i, err)
		}
	}
	return nil
}

func validateSSHEnvPattern(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("must not be empty")
	}
	if strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\r\n\x00=") {
		return fmt.Errorf("invalid environment name or pattern: %q", name)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '*' || r == '?' {
			continue
		}
		return fmt.Errorf("invalid environment name or pattern: %q", name)
	}
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"SSH_", "PICOCLAW_", "DOCKER_", "XDG_"} {
		if strings.HasPrefix(upper, prefix) {
			return fmt.Errorf("%q is managed by PicoClaw or the ssh client", name)
		}
	}
	if !strings.ContainsAny(name, "*?") && sshReservedEnv(name) {
		return fmt.Errorf("%q is set by the remote login shell", name)
	}
	return nil
}

func sshReservedEnv(name string) bool {
	switch strings.ToUpper(name) {
	case "HOME", "PATH", "PWD", "OLDPWD", "SHELL", "USER", "LOGNAME",
		"SHLVL", "TMPDIR", "_":
		return true
	}
	return false
}

func validateSSHExtraArg(arg string) error {
	if strings.TrimSpace(arg) == "" {
		return fmt.Errorf("must not be empty")
	}
	if strings.ContainsRune(arg, 0) {
		return fmt.Errorf("must not contain a NUL byte")
	}
	if arg == "--" {
		return fmt.Errorf("must not terminate ssh options: %q", arg)
	}
	if sshLifecycleFlag(arg) {
		return fmt.Errorf("must not override ssh target, command or transport: %q", arg)
	}
	return nil
}

// sshLifecycleFlag rejects extra arguments that would change the target,
// consume the remote command, forward ports or the local agent, or take over
// the controlling terminal. The config is trusted, but these flags break the
// backend's command and isolation model.
func sshLifecycleFlag(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	// -oName=value is a single option value; it is trusted configuration, and
	// scanning the value would reject valid names such as -oProxyJump=bastion.
	if strings.HasPrefix(arg, "-o") && len(arg) > 2 {
		return false
	}
	for _, r := range arg[1:] {
		switch r {
		case 'W', 'O', 'S', 'N', 'f', 'G', 'Q', 'V', 'h',
			'p', 'i', 'l', 'A', 'L', 'R', 'D', 'X', 'Y', 't', 'T':
			return true
		}
	}
	return false
}

func preflightSSH(cfg config.SSHIsolationConfig, expose []config.ExposePath) error {
	if err := validateSSHIsolation(config.IsolationConfig{
		Enabled:     true,
		Backend:     config.IsolationBackendSSH,
		ExposePaths: expose,
		SSH:         cfg,
	}); err != nil {
		return err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh isolation requires the OpenSSH client on PATH: %w", err)
	}
	// The probe starts the ControlMaster that subsequent commands reuse. It
	// must use the same SendEnv name set: a mux master only relays the names it
	// was started with and silently drops later ones.
	args := buildSSHArgs(cfg, sshForwardEnvNames(cfg), "true", false)
	ctx, cancel := context.WithTimeout(context.Background(), sshPreflightTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sshPath, args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"ssh isolation requires a reachable SSH server at %s: %w: %s; verify the host, port, key or agent, and known_hosts",
			cfg.Target(),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}
