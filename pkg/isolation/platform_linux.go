//go:build linux

package isolation

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled {
		return nil
	}
	// Bubblewrap is the only supported Linux backend right now. Fail closed when
	// it is unavailable instead of silently running the child process unisolated.
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		hint := bwrapInstallHint()
		disableHint := `set "isolation.enabled": false in config.json`
		logger.WarnCF("isolation", "bubblewrap is required for Linux isolation",
			map[string]any{
				"binary":            "bwrap",
				"install":           hint,
				"disable_isolation": disableHint,
				"risk":              "disabling isolation lets child processes run without Linux filesystem isolation",
			})
		return fmt.Errorf(
			"linux isolation requires bwrap and does not fall back automatically: %w; install bubblewrap with one of: %s; or disable isolation by setting %s; disabling isolation means child processes can run without Linux filesystem isolation and may access or modify more host files",
			err,
			hint,
			disableHint,
		)
	}
	if versionErr := checkBwrapVersion(bwrapPath, func(path string, args ...string) ([]byte, error) {
		return exec.Command(path, args...).Output()
	}); versionErr != nil {
		return versionErr
	}
	if cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return nil
	}
	namespaceFlags, err := cachedLinuxNamespaceFlags(bwrapPath)
	if err != nil {
		return err
	}

	originalPath := cmd.Path
	originalArgs := append([]string{}, cmd.Args...)
	_, execDir, err := resolveLinuxWorkingDir(cmd.Dir, originalPath)
	if err != nil {
		return err
	}
	resolvedPath, err := resolveLinuxCommandPath(originalPath, execDir)
	if err != nil {
		return err
	}

	// Start from the configured mount plan, then add only the executable, its
	// resolved path, the effective working directory, and any absolute path
	// arguments needed to preserve the original command semantics.
	plan := BuildLinuxMountPlan(root, isolation.ExposePaths)
	plan = appendLinuxCommandMountRules(plan, resolvedPath)
	if execDir != "" {
		plan = ensureLinuxMountRule(plan, execDir, execDir, "rw")
		if resolved, resolveErr := filepath.EvalSymlinks(execDir); resolveErr == nil && resolved != execDir {
			plan = ensureLinuxMountRule(plan, resolved, resolved, "rw")
		}
	}
	plan = appendLinuxArgumentMounts(plan, originalArgs[1:])
	if guardErr := checkSymlinkMountTargets(plan); guardErr != nil {
		return guardErr
	}
	logger.DebugCF("isolation", "linux isolation mount plan",
		map[string]any{
			"root":        root,
			"command":     resolvedPath,
			"working_dir": execDir,
			"mounts":      formatLinuxMountPlan(plan),
		})
	bwrapArgs, err := buildLinuxBwrapArgs(originalPath, resolvedPath, originalArgs, execDir, plan, namespaceFlags)
	if err != nil {
		return err
	}
	if isolation.BlockNetwork {
		bwrapArgs = insertLinuxUnshareNet(bwrapArgs)
	}

	cmd.Path = bwrapPath
	cmd.Args = bwrapArgs
	cmd.Dir = ""
	return nil
}

func bwrapInstallHint() string {
	return "apt install bubblewrap; dnf install bubblewrap; yum install bubblewrap; pacman -S bubblewrap; apk add bubblewrap"
}

// formatLinuxMountPlan reshapes the internal plan for structured logging.
func formatLinuxMountPlan(plan []MountRule) []map[string]string {
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

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}

func cleanupPendingPlatformResources(cmd *exec.Cmd) {
}

// Unsupported optional namespaces are omitted so mount and filesystem isolation
// can still run; the dropped namespace is an explicit security tradeoff.
var linuxOptionalNamespaceFlags = []string{"--unshare-ipc"}

var linuxNamespaceProbe struct {
	once  sync.Once
	flags map[string]bool
	err   error
}

func cachedLinuxNamespaceFlags(bwrapPath string) (map[string]bool, error) {
	linuxNamespaceProbe.once.Do(func() {
		run := func(path string, args ...string) ([]byte, error) {
			var stderr bytes.Buffer
			cmd := exec.Command(path, args...)
			cmd.Stderr = &stderr
			err := cmd.Run()
			return stderr.Bytes(), err
		}
		// PID namespace is required: fail closed if the probe fails rather than
		// running without it and silently sharing the host PID table.
		if err := probeLinuxPIDNamespace(bwrapPath, run); err != nil {
			linuxNamespaceProbe.err = err
			return
		}
		linuxNamespaceProbe.flags, linuxNamespaceProbe.err = probeLinuxNamespaceFlags(
			bwrapPath,
			run,
		)
		if linuxNamespaceProbe.err != nil {
			return
		}
		for _, flag := range linuxOptionalNamespaceFlags {
			if !linuxNamespaceProbe.flags[flag] {
				logger.WarnCF(
					"isolation",
					"bubblewrap dropped "+flag+"; corresponding namespace isolation is not active",
					map[string]any{
						"flag":                flag,
						"namespace_isolation": "not active",
						"reason":              "kernel lacks this namespace; bwrap returned 'Creating new namespace failed' (EINVAL)",
						"remaining_isolation": "mount namespace, filesystem view, and user environment remain active",
					},
				)
			}
		}
	})
	return linuxNamespaceProbe.flags, linuxNamespaceProbe.err
}

func probeLinuxNamespaceFlags(
	bwrapPath string,
	run func(string, ...string) ([]byte, error),
) (map[string]bool, error) {
	flags := make(map[string]bool, len(linuxOptionalNamespaceFlags))
	for _, flag := range linuxOptionalNamespaceFlags {
		output, err := run(bwrapPath,
			"--ro-bind", "/", "/",
			"--dev", "/dev",
			"--proc", "/proc",
			flag,
			"--", "true",
		)
		if err == nil {
			flags[flag] = true
			continue
		}
		if strings.Contains(string(output), "Creating new namespace failed") {
			flags[flag] = false
			continue
		}
		return nil, fmt.Errorf(
			"probe bubblewrap namespace flag %s: %w: %s",
			flag,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return flags, nil
}

// buildLinuxBwrapArgs translates the mount plan into the bubblewrap command
// line that re-executes the original process inside the isolated mount view.
func buildLinuxBwrapArgs(
	originalPath string,
	resolvedPath string,
	originalArgs []string,
	execDir string,
	plan []MountRule,
	namespaceFlags map[string]bool,
) ([]string, error) {
	bwrapArgs := []string{
		"bwrap",
		"--die-with-parent",
		// --new-session calls setsid(2) in the sandbox so it has no controlling
		// terminal and cannot inject characters into the caller's terminal via
		// TIOCSTI (CVE-2017-5226).
		"--new-session",
	}
	bwrapArgs = append(bwrapArgs, "--unshare-pid")
	for _, flag := range linuxOptionalNamespaceFlags {
		if namespaceFlags[flag] {
			bwrapArgs = append(bwrapArgs, flag)
		}
	}
	bwrapArgs = append(bwrapArgs, "--proc", "/proc", "--dev", "/dev")
	for _, rule := range plan {
		flag, err := linuxBindFlag(rule)
		if err != nil {
			return nil, err
		}
		bwrapArgs = append(bwrapArgs, flag, rule.Source, rule.Target)
	}
	if execDir != "" {
		bwrapArgs = append(bwrapArgs, "--chdir", execDir)
	}
	execPath := originalPath
	if isRelativeCommandPath(originalPath) {
		execPath = resolvedPath
	}
	bwrapArgs = append(bwrapArgs, "--", execPath)
	if len(originalArgs) > 1 {
		bwrapArgs = append(bwrapArgs, originalArgs[1:]...)
	}
	return bwrapArgs, nil
}

func resolveLinuxWorkingDir(originalDir, originalPath string) (string, string, error) {
	if originalDir != "" {
		resolved, err := filepath.Abs(originalDir)
		if err != nil {
			return "", "", fmt.Errorf("resolve command dir %s: %w", originalDir, err)
		}
		return resolved, resolved, nil
	}
	if !isRelativeCommandPath(originalPath) {
		return "", "", nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("resolve current working dir: %w", err)
	}
	return "", wd, nil
}

func resolveLinuxCommandPath(originalPath, execDir string) (string, error) {
	if filepath.IsAbs(originalPath) || !isRelativeCommandPath(originalPath) {
		return filepath.Clean(originalPath), nil
	}
	base := execDir
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current working dir: %w", err)
		}
	}
	return filepath.Clean(filepath.Join(base, originalPath)), nil
}

func appendLinuxArgumentMounts(plan []MountRule, args []string) []MountRule {
	for _, arg := range args {
		path, ok := linuxArgumentPath(arg)
		if !ok {
			continue
		}
		plan = appendLinuxArgumentMountsFor(plan, path)
	}
	return plan
}

func linuxArgumentPath(arg string) (string, bool) {
	// Plain absolute path token: must contain no shell metacharacters so that
	// command strings like "cat /home/bob/x; rm -rf /" are never treated as
	// paths. See isLinuxArgPathCandidate in arg_mounts_linux.go.
	if path, ok := isLinuxArgPathCandidate(arg); ok {
		return path, true
	}
	// --flag=value form: extract the value and apply the same candidate check.
	idx := strings.IndexRune(arg, '=')
	if idx <= 0 || idx == len(arg)-1 {
		return "", false
	}
	return isLinuxArgPathCandidate(arg[idx+1:])
}

func isRelativeCommandPath(path string) bool {
	return !filepath.IsAbs(path) && strings.ContainsRune(path, filepath.Separator)
}

// linuxBindFlag selects the correct bubblewrap bind flag based on mount mode.
func linuxBindFlag(rule MountRule) (string, error) {
	info, err := os.Stat(rule.Source)
	if err != nil {
		return "", fmt.Errorf("stat linux mount source %s: %w", rule.Source, err)
	}
	if !info.IsDir() {
		if rule.Mode == "rw" {
			return "--bind", nil
		}
		return "--ro-bind", nil
	}
	if rule.Mode == "rw" {
		return "--bind", nil
	}
	return "--ro-bind", nil
}
