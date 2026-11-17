package isolation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
)

// MountRule describes a source-to-target mount exposed inside the Linux
// isolation view.
type MountRule struct {
	Source string
	Target string
	Mode   string
}

// AccessRule describes the effective Windows-side access rule for a host path.
type AccessRule struct {
	Path string
	Mode string
}

// UserEnv contains the redirected per-instance user directories injected into
// isolated child processes.
type UserEnv struct {
	Home         string
	Tmp          string
	Config       string
	Cache        string
	State        string
	AppData      string
	LocalAppData string
}

var (
	isolationMu      sync.RWMutex
	currentIsolation = config.DefaultConfig().Isolation
	currentWorkspace = config.DefaultConfig().WorkspacePath()
	isolationGen     uint64

	// preflightCache memoizes the expensive Preflight work (instance dir
	// creation and expose-path validation) so it is not repeated for every
	// child process. It is invalidated whenever Configure changes the config.
	preflightMu    sync.Mutex
	preflightCache preflightResult
)

type preflightResult struct {
	done bool
	gen  uint64
	err  error
}

// Configure updates the process-wide isolation state used by subsequent child
// process launches.
func Configure(cfg *config.Config) {
	isolationMu.Lock()
	defer isolationMu.Unlock()
	if cfg == nil {
		defaults := config.DefaultConfig()
		currentIsolation = defaults.Isolation
		currentWorkspace = absoluteWorkspace(defaults.WorkspacePath())
	} else {
		currentIsolation = cfg.Isolation
		currentWorkspace = absoluteWorkspace(cfg.WorkspacePath())
	}
	isolationGen++
}

// ConfigureForWorkspace is Configure plus the resolved agent workspace. The
// exec tool passes its own workspace to StartForWorkspace, so this is only the
// default for integrations (hooks, MCP, CLI providers) that do not carry one.
func ConfigureForWorkspace(cfg *config.Config, workspace string) {
	isolationMu.Lock()
	defer isolationMu.Unlock()
	if cfg == nil {
		defaults := config.DefaultConfig()
		currentIsolation = defaults.Isolation
		currentWorkspace = absoluteWorkspace(defaults.WorkspacePath())
	} else {
		currentIsolation = cfg.Isolation
		currentWorkspace = absoluteWorkspace(cfg.WorkspacePath())
	}
	if workspace != "" {
		currentWorkspace = absoluteWorkspace(workspace)
	}
	isolationGen++
}

// CurrentConfig returns the currently active isolation settings.
func CurrentConfig() config.IsolationConfig {
	isolationMu.RLock()
	defer isolationMu.RUnlock()
	return currentIsolation
}

// CurrentWorkspace returns the workspace used by isolation callers that do not
// pass an explicit workspace.
func CurrentWorkspace() string {
	isolationMu.RLock()
	defer isolationMu.RUnlock()
	return currentWorkspace
}

func absoluteWorkspace(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// ValidateWorkspace rejects a workspace that would turn the isolation root back
// into a writable view of the whole instance root (or the whole host).
func ValidateWorkspace(root, workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace %q: %w", workspace, err)
	}
	absWorkspace = filepath.Clean(absWorkspace)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve instance root %q: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)

	if absWorkspace == string(filepath.Separator) {
		return fmt.Errorf("workspace must not be the filesystem root")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		cleanHome := filepath.Clean(home)
		if absWorkspace == cleanHome {
			return fmt.Errorf("workspace must not be the user home directory")
		}
	}
	if absWorkspace == absRoot {
		return fmt.Errorf("workspace must not be the instance root")
	}
	for _, system := range []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/root"} {
		if absWorkspace == system || boundaryPathWithin(system, absWorkspace) {
			return fmt.Errorf("workspace %s must not be inside the system directory %s", absWorkspace, system)
		}
	}
	if boundaryPathWithin(absWorkspace, absRoot) {
		// workspace is an ancestor of the instance root: mounting it RW would
		// expose config/state/skills again.
		return fmt.Errorf("workspace %s contains the instance root %s", absWorkspace, absRoot)
	}
	for _, protected := range []string{"state", "skills", "logs", "cache", "plugins"} {
		protectedPath := filepath.Join(absRoot, protected)
		if boundaryPathWithin(protectedPath, absWorkspace) {
			return fmt.Errorf("workspace %s is inside protected instance directory %s", absWorkspace, protectedPath)
		}
	}
	return nil
}

// ResolveInstanceRoot resolves the instance root used to build the isolated
// filesystem and redirected user environment.
func ResolveInstanceRoot() (string, error) {
	root := filepath.Clean(config.GetHome())
	if root == "." {
		return "", fmt.Errorf("instance root resolved to current directory")
	}
	return root, nil
}

// PrepareInstanceRoot creates the directories required by the isolation runtime.
func PrepareInstanceRoot(root string) error {
	for _, dir := range InstanceDirs(root) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("prepare instance dir %s: %w", dir, err)
		}
	}
	return nil
}

// InstanceDirs returns the directories that must exist under the instance root
// for isolation-aware child processes.
func InstanceDirs(root string) []string {
	dirs := []string{
		root,
		filepath.Join(root, "skills"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "state"),
		filepath.Join(root, "runtime-user-env"),
		filepath.Join(root, "runtime-user-env", "home"),
		filepath.Join(root, "runtime-user-env", "tmp"),
		filepath.Join(root, "runtime-user-env", "config"),
		filepath.Join(root, "runtime-user-env", "cache"),
		filepath.Join(root, "runtime-user-env", "state"),
	}
	dirs = append(dirs, filepath.Join(root, pkg.WorkspaceName))
	if runtime.GOOS == "windows" {
		dirs = append(dirs,
			filepath.Join(root, "runtime-user-env", "AppData", "Roaming"),
			filepath.Join(root, "runtime-user-env", "AppData", "Local"),
		)
	}
	return dirs
}

// ResolveUserEnv derives the redirected user directories rooted under the
// instance runtime area.
func ResolveUserEnv(root string) UserEnv {
	base := filepath.Join(root, "runtime-user-env")
	return UserEnv{
		Home:         filepath.Join(base, "home"),
		Tmp:          filepath.Join(base, "tmp"),
		Config:       filepath.Join(base, "config"),
		Cache:        filepath.Join(base, "cache"),
		State:        filepath.Join(base, "state"),
		AppData:      filepath.Join(base, "AppData", "Roaming"),
		LocalAppData: filepath.Join(base, "AppData", "Local"),
	}
}

// ApplyUserEnv rewrites the child process environment so home, temp, and
// platform-specific user-data directories point into the instance root.
func ApplyUserEnv(cmd *exec.Cmd, root string) {
	userEnv := ResolveUserEnv(root)
	envMap := make(map[string]string)
	for _, item := range cmd.Environ() {
		if idx := strings.IndexRune(item, '='); idx > 0 {
			envMap[item[:idx]] = item[idx+1:]
		}
	}

	if runtime.GOOS == "windows" {
		envMap["USERPROFILE"] = userEnv.Home
		envMap["HOME"] = userEnv.Home
		envMap["TEMP"] = userEnv.Tmp
		envMap["TMP"] = userEnv.Tmp
		envMap["APPDATA"] = userEnv.AppData
		envMap["LOCALAPPDATA"] = userEnv.LocalAppData
	} else {
		envMap["HOME"] = userEnv.Home
		envMap["TMPDIR"] = userEnv.Tmp
		envMap["XDG_CONFIG_HOME"] = userEnv.Config
		envMap["XDG_CACHE_HOME"] = userEnv.Cache
		envMap["XDG_STATE_HOME"] = userEnv.State
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env
}

// ValidateExposePaths verifies the user-supplied path exposure rules before a
// child process is started.
func ValidateExposePaths(items []config.ExposePath) error {
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.Source == "" {
			return fmt.Errorf("source is required")
		}
		if item.Mode != "ro" && item.Mode != "rw" {
			return fmt.Errorf("invalid expose_paths mode: %s", item.Mode)
		}

		source := filepath.Clean(item.Source)
		target := item.Target
		if target == "" {
			target = source
		}
		target = filepath.Clean(target)

		if !filepath.IsAbs(source) || !filepath.IsAbs(target) {
			return fmt.Errorf("source and target must be absolute paths")
		}
		if _, ok := seen[target]; ok {
			return fmt.Errorf("duplicate expose_path target: %s", target)
		}
		seen[target] = struct{}{}
	}
	return nil
}

// NormalizeExposePath fills implicit defaults and cleans path values so merge
// and validation logic can work with canonical paths.
func NormalizeExposePath(item config.ExposePath) config.ExposePath {
	source := filepath.Clean(item.Source)
	target := item.Target
	if target == "" {
		target = source
	}
	return config.ExposePath{
		Source: source,
		Target: filepath.Clean(target),
		Mode:   item.Mode,
	}
}

// DefaultExposePaths returns the minimum built-in host paths required for the
// current platform to run isolated child processes. The instance root is
// read-only; callers that need workspace/scratch writes use
// DefaultWorkspaceExposePaths.
func DefaultExposePaths(root string) []config.ExposePath {
	items := []config.ExposePath{{
		Source: root,
		Target: root,
		Mode:   "ro",
	}}
	if runtime.GOOS == "linux" {
		items = append(items, defaultLinuxSystemExposePaths()...)
	}
	return items
}

// DefaultWorkspaceExposePaths returns the read-only instance root plus the two
// writable areas an exec child actually needs: its workspace and the redirected
// per-instance user environment. Config, state, skills and logs stay RO because
// they live under the read-only root.
func DefaultWorkspaceExposePaths(root, workspace string) []config.ExposePath {
	items := []config.ExposePath{{
		Source: root,
		Target: root,
		Mode:   "ro",
	}}
	cleanWorkspace := absoluteWorkspace(workspace)
	if cleanWorkspace != "" && cleanWorkspace != filepath.Clean(root) {
		items = append(items, config.ExposePath{
			Source: cleanWorkspace,
			Target: cleanWorkspace,
			Mode:   "rw",
		})
	}
	runtimeEnv := filepath.Join(root, "runtime-user-env")
	items = append(items, config.ExposePath{
		Source: runtimeEnv,
		Target: runtimeEnv,
		Mode:   "rw",
	})
	if runtime.GOOS == "linux" {
		items = append(items, defaultLinuxSystemExposePaths()...)
	}
	return items
}

func defaultLinuxSystemExposePaths() []config.ExposePath {
	return existingExposePaths([]config.ExposePath{
		{Source: "/usr", Target: "/usr", Mode: "ro"},
		{Source: "/bin", Target: "/bin", Mode: "ro"},
		{Source: "/lib", Target: "/lib", Mode: "ro"},
		{Source: "/lib64", Target: "/lib64", Mode: "ro"},
		{Source: "/etc/resolv.conf", Target: "/etc/resolv.conf", Mode: "ro"},
		{Source: "/etc/hosts", Target: "/etc/hosts", Mode: "ro"},
		{Source: "/etc/nsswitch.conf", Target: "/etc/nsswitch.conf", Mode: "ro"},
		{Source: "/etc/passwd", Target: "/etc/passwd", Mode: "ro"},
		{Source: "/etc/group", Target: "/etc/group", Mode: "ro"},
		{Source: "/etc/ssl", Target: "/etc/ssl", Mode: "ro"},
		{Source: "/etc/pki", Target: "/etc/pki", Mode: "ro"},
		{Source: "/etc/ca-certificates", Target: "/etc/ca-certificates", Mode: "ro"},
		{Source: "/usr/share/ca-certificates", Target: "/usr/share/ca-certificates", Mode: "ro"},
		{Source: "/usr/local/share/ca-certificates", Target: "/usr/local/share/ca-certificates", Mode: "ro"},
		{Source: "/etc/alternatives", Target: "/etc/alternatives", Mode: "ro"},
		{Source: "/usr/share/zoneinfo", Target: "/usr/share/zoneinfo", Mode: "ro"},
		{Source: "/etc/localtime", Target: "/etc/localtime", Mode: "ro"},
	})
}

// existingExposePaths keeps only the builtin host paths that exist on the
// current machine so Linux isolation does not fail on distro-specific paths.
func existingExposePaths(items []config.ExposePath) []config.ExposePath {
	filtered := make([]config.ExposePath, 0, len(items))
	for _, item := range items {
		if _, err := os.Stat(item.Source); err == nil {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// MergeExposePaths merges built-in rules with user overrides. Rules are keyed
// by target path so later entries replace earlier ones for the same target.
func MergeExposePaths(defaults []config.ExposePath, overrides []config.ExposePath) []config.ExposePath {
	merged := make([]config.ExposePath, 0, len(defaults)+len(overrides))
	indexByTarget := make(map[string]int, len(defaults)+len(overrides))
	appendOrReplace := func(item config.ExposePath) {
		normalized := NormalizeExposePath(item)
		if idx, ok := indexByTarget[normalized.Target]; ok {
			merged[idx] = normalized
			return
		}
		indexByTarget[normalized.Target] = len(merged)
		merged = append(merged, normalized)
	}
	for _, item := range defaults {
		appendOrReplace(item)
	}
	for _, item := range overrides {
		appendOrReplace(item)
	}
	return merged
}

// BuildLinuxMountPlan converts the merged expose-path configuration into the
// mount rules consumed by the Linux bubblewrap backend. The instance root is
// read-only; only the workspace and runtime-user-env are writable by default.
func BuildLinuxMountPlan(root, workspace string, overrides []config.ExposePath) []MountRule {
	merged := MergeExposePaths(DefaultWorkspaceExposePaths(root, workspace), overrides)
	plan := make([]MountRule, 0, len(merged))
	for _, item := range merged {
		plan = append(plan, MountRule{Source: item.Source, Target: item.Target, Mode: item.Mode})
	}
	return plan
}

// appendLinuxCommandMountRules exposes an executable to the bubblewrap sandbox.
// It always mounts the parent directory so the command path exists in the new
// root. A symlink path itself is not mounted: bubblewrap 0.10 refuses to bind
// over a symlink destination ("Can't mount on symlink destination"), so the
// link is made visible through its parent and the resolved target is mounted
// instead when it lives outside that parent.
func appendLinuxCommandMountRules(plan []MountRule, resolvedPath string) []MountRule {
	if mountPlanAllowsWrite(plan, resolvedPath) {
		return plan
	}
	if !isSymlinkPath(resolvedPath) {
		plan = ensureLinuxMountRule(plan, resolvedPath, resolvedPath, "ro")
	}
	if dir := filepath.Dir(resolvedPath); !isSandboxTmpPath(dir) {
		plan = ensureLinuxMountRule(plan, dir, dir, "ro")
	}

	resolved, err := filepath.EvalSymlinks(resolvedPath)
	if err != nil || resolved == resolvedPath {
		return plan
	}
	if !isSymlinkPath(resolved) {
		plan = ensureLinuxMountRule(plan, resolved, resolved, "ro")
	}
	if dir := filepath.Dir(resolved); !isSandboxTmpPath(dir) {
		plan = ensureLinuxMountRule(plan, dir, dir, "ro")
	}
	return plan
}

func isSymlinkPath(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// ensureLinuxMountRule appends a mount rule unless another rule already owns
// the same target path.
func ensureLinuxMountRule(plan []MountRule, source, target, mode string) []MountRule {
	cleanSource := filepath.Clean(source)
	cleanTarget := filepath.Clean(target)
	for _, rule := range plan {
		if filepath.Clean(rule.Target) == cleanTarget {
			return plan
		}
	}
	return append(plan, MountRule{Source: cleanSource, Target: cleanTarget, Mode: mode})
}

// mountPlanAllowsWrite reports whether path is already covered by a writable
// mount rule. Absolute command arguments that point inside a writable area must
// not be re-mounted read-only: doing so would silently break writes to a file
// the command received as an argument.
func mountPlanAllowsWrite(plan []MountRule, path string) bool {
	clean := filepath.Clean(path)
	for _, rule := range plan {
		if rule.Mode == "rw" && boundaryPathWithin(rule.Target, clean) {
			return true
		}
	}
	return false
}

// isSandboxTmpPath reports whether path is the sandbox /tmp or below it. The
// Linux backend mounts an ephemeral tmpfs on /tmp; host /tmp must never be
// bind-mounted RW.
func isSandboxTmpPath(path string) bool {
	return filepath.Clean(path) == "/tmp"
}

func BuildWindowsAccessRules(root, workspace string, overrides []config.ExposePath) []AccessRule {
	rules := []AccessRule{{Path: root, Mode: "ro"}}
	cleanWorkspace := absoluteWorkspace(workspace)
	if cleanWorkspace != "" && cleanWorkspace != filepath.Clean(root) {
		rules = append(rules, AccessRule{Path: cleanWorkspace, Mode: "rw"})
	}
	runtimeEnv := filepath.Join(root, "runtime-user-env")
	rules = append(rules, AccessRule{Path: runtimeEnv, Mode: "rw"})
	for _, item := range overrides {
		rules = append(rules, AccessRule{Path: item.Source, Mode: item.Mode})
	}
	return rules
}

func validateWindowsExposePaths(items []config.ExposePath) error {
	if len(items) == 0 {
		return nil
	}
	return fmt.Errorf("windows isolation does not yet support expose_paths filesystem rules")
}

func validateDarwinExposePaths(items []config.ExposePath, root string) error {
	for _, item := range items {
		path := NormalizeExposePath(item)
		if path.Source != path.Target {
			return fmt.Errorf(
				"macOS Seatbelt does not support expose_paths remapping: %s -> %s",
				path.Source,
				path.Target,
			)
		}
		if path.Mode == "rw" && boundaryPathWithin(path.Target, root) {
			return fmt.Errorf(
				"macOS Seatbelt cannot make %s writable: it is the instance root or an ancestor of it and would expose config/state/skills again",
				path.Target,
			)
		}
	}
	return nil
}

// IsSupported reports whether the current platform has an implemented isolation
// backend for the active configuration. Docker is available on every platform
// where the Docker CLI and daemon are reachable; preflight validates them.
func IsSupported() bool {
	return isBackendSupportedOn(runtime.GOOS, resolvedBackend(CurrentConfig()))
}

func isSupportedOn(goos string) bool {
	switch goos {
	case "linux", "windows", "darwin":
		return true
	default:
		return false
	}
}

// Preflight validates the configured isolation state and prepares the instance
// runtime directories before any child process is launched.
func Preflight() error {
	return preflight(CurrentConfig())
}

// preflightCached runs Preflight at most once per configuration generation.
// Failures are cached too: if isolation cannot be prepared, it will not become
// possible later in the same process without a Configure call.
func preflightCached() error {
	isolationMu.RLock()
	gen := isolationGen
	isolation := currentIsolation
	isolationMu.RUnlock()

	preflightMu.Lock()
	defer preflightMu.Unlock()
	if preflightCache.done && preflightCache.gen == gen {
		return preflightCache.err
	}
	err := preflight(isolation)
	preflightCache = preflightResult{done: true, gen: gen, err: err}
	return err
}

func preflight(isolation config.IsolationConfig) error {
	if !isolation.Enabled {
		return nil
	}
	backend := resolvedBackend(isolation)
	if err := validateBackend(isolation); err != nil {
		return err
	}
	root, err := ResolveInstanceRoot()
	if err != nil {
		return err
	}
	workspace := CurrentWorkspace()
	if err := ValidateWorkspace(root, workspace); err != nil {
		return err
	}
	if err := PrepareInstanceRoot(root); err != nil {
		return err
	}
	if err := ValidateExposePaths(isolation.ExposePaths); err != nil {
		return err
	}
	switch backend {
	case config.IsolationBackendDocker:
		return preflightDocker(isolation.Docker, root, workspace, isolation.ExposePaths)
	case config.IsolationBackendSSH:
		return preflightSSH(isolation.SSH, workspace, isolation.ExposePaths)
	case config.IsolationBackendBubblewrap:
		for _, rule := range BuildLinuxMountPlan(root, workspace, isolation.ExposePaths) {
			if rule.Source == "" || rule.Target == "" {
				return fmt.Errorf("invalid linux mount rule")
			}
		}
	case config.IsolationBackendWindows:
		if err := validateWindowsExposePaths(isolation.ExposePaths); err != nil {
			return err
		}
		for _, rule := range BuildWindowsAccessRules(root, workspace, isolation.ExposePaths) {
			if rule.Path == "" {
				return fmt.Errorf("invalid windows access rule")
			}
		}
	case config.IsolationBackendSeatbelt:
		if err := validateDarwinExposePaths(isolation.ExposePaths, root); err != nil {
			return err
		}
	}
	return nil
}

// Start prepares isolation for the command, starts it, and applies any
// post-start platform hooks required by the active backend. Callers must call
// Release(cmd) after the process exits to discard per-command bookkeeping.
func Start(cmd *exec.Cmd) error {
	return StartForWorkspace(cmd, CurrentWorkspace())
}

// StartForWorkspace is Start with an explicit workspace. The exec tool uses it
// so the writable mount follows the agent's workspace instead of a process-wide
// default.
func StartForWorkspace(cmd *exec.Cmd, workspace string) error {
	if err := PrepareCommandForWorkspace(cmd, workspace); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		cleanupPendingPlatformResources(cmd)
		return err
	}
	isolation := CurrentConfig()
	root := ""
	if isolation.Enabled {
		var err error
		root, err = ResolveInstanceRoot()
		if err != nil {
			terminateStartedCommand(cmd)
			return err
		}
	}
	if err := postStartPlatformIsolation(cmd, isolation, root); err != nil {
		terminateStartedCommand(cmd)
		return err
	}
	return nil
}

// Run is the Start-and-Wait helper that keeps the same isolation behavior as
// Start while returning the command's final exit status. It releases the
// per-command bookkeeping automatically.
func Run(cmd *exec.Cmd) error {
	return RunForWorkspace(cmd, CurrentWorkspace())
}

// RunForWorkspace is Run with an explicit workspace.
func RunForWorkspace(cmd *exec.Cmd, workspace string) error {
	if err := PrepareCommandForWorkspace(cmd, workspace); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		cleanupPendingPlatformResources(cmd)
		return err
	}
	isolation := CurrentConfig()
	root := ""
	if isolation.Enabled {
		var err error
		root, err = ResolveInstanceRoot()
		if err != nil {
			terminateStartedCommand(cmd)
			return err
		}
	}
	if err := postStartPlatformIsolation(cmd, isolation, root); err != nil {
		terminateStartedCommand(cmd)
		return err
	}
	defer Release(cmd)
	err := cmd.Wait()
	if err != nil && cmd.Process != nil && cmd.ProcessState != nil &&
		cmd.ProcessState.ExitCode() < 0 {
		// A killed or interrupted docker CLI does not stop its container.
		CleanupPID(cmd.Process.Pid)
	}
	return err
}

func terminateStartedCommand(cmd *exec.Cmd) {
	cleanupPendingPlatformResources(cmd)
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// PrepareCommand mutates the command in-place so it inherits the configured
// isolated environment before being started by the caller.
func PrepareCommand(cmd *exec.Cmd) error {
	return PrepareCommandForWorkspace(cmd, CurrentWorkspace())
}

// PrepareCommandForWorkspace is PrepareCommand with an explicit workspace.
func PrepareCommandForWorkspace(cmd *exec.Cmd, workspace string) error {
	isolation := CurrentConfig()
	if err := preflightCached(); err != nil {
		return err
	}
	if isolation.Enabled {
		root, err := ResolveInstanceRoot()
		if err != nil {
			return err
		}
		if workspace == "" {
			workspace = CurrentWorkspace()
		}
		if err := ValidateWorkspace(root, workspace); err != nil {
			return err
		}
		ApplyUserEnv(cmd, root)
		if err := applyPlatformIsolation(cmd, isolation, root, workspace); err != nil {
			return err
		}
	}
	return nil
}
