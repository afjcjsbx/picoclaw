//go:build darwin

package isolation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled || cmd == nil || cmd.Path == "" || len(cmd.Args) == 0 {
		return nil
	}
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return fmt.Errorf("macOS isolation requires sandbox-exec: %w", err)
	}

	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	profile := buildDarwinSeatbeltProfile(root, cmd, isolation.ExposePaths)
	logger.DebugCF("isolation", "macOS Seatbelt profile prepared", map[string]any{
		"root": root, "command": originalPath, "expose_paths": len(isolation.ExposePaths),
	})
	cmd.Path = sandboxExec
	cmd.Args = append([]string{"sandbox-exec", "-p", profile, originalPath}, originalArgs[1:]...)
	return nil
}

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}

func cleanupPendingPlatformResources(cmd *exec.Cmd) {}

func buildDarwinSeatbeltProfile(root string, cmd *exec.Cmd, exposePaths []config.ExposePath) string {
	var rules strings.Builder
	addPath := func(path, mode, predicate string, executable bool) {
		if !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		quoted := strconv.Quote(path)
		fmt.Fprintf(&rules, "(allow file-read* file-test-existence (%s %s))\n", predicate, quoted)
		if mode == "rw" {
			fmt.Fprintf(&rules, "(allow file-write* (%s %s))\n", predicate, quoted)
		}
		if executable {
			fmt.Fprintf(&rules, "(allow file-map-executable (%s %s))\n", predicate, quoted)
		}
	}
	addTree := func(path, mode string, executable bool) {
		addPath(path, mode, "subpath", executable)
		if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
			addPath(resolved, mode, "subpath", executable)
		}
	}

	profile := strings.Builder{}
	profile.WriteString("(version 1)\n(deny default)\n(import \"system.sb\")\n")
	profile.WriteString("(allow process-exec process-fork network*)\n(system-network)\n")
	for _, path := range []string{"/System", "/bin", "/sbin", "/etc", "/usr/bin", "/usr/sbin", "/usr/lib", "/usr/libexec", "/usr/share", "/Library/Apple"} {
		addTree(path, "ro", path != "/usr/share")
	}
	addTree(root, "rw", true)
	for _, item := range exposePaths {
		path := NormalizeExposePath(item)
		mode := "ro"
		if path.Mode == "rw" {
			mode = "rw"
		}
		if info, err := os.Stat(path.Source); err == nil && info.IsDir() {
			addTree(path.Source, mode, true)
		} else {
			addPath(path.Source, mode, "literal", err == nil && info.Mode().Perm()&0o111 != 0)
		}
	}

	commandPath := cmd.Path
	base := cmd.Dir
	if base == "" {
		base, _ = os.Getwd()
	} else if !filepath.IsAbs(base) {
		cwd, _ := os.Getwd()
		base = filepath.Join(cwd, base)
	}
	if !filepath.IsAbs(commandPath) {
		commandPath = filepath.Join(base, commandPath)
	}
	commandPath = filepath.Clean(commandPath)
	addPath(commandPath, "ro", "literal", true)
	addTree(filepath.Dir(commandPath), "ro", true)
	if resolved, err := filepath.EvalSymlinks(commandPath); err == nil && resolved != commandPath {
		addPath(resolved, "ro", "literal", true)
		addTree(filepath.Dir(resolved), "ro", true)
	}
	if cmd.Dir != "" {
		workingDir := cmd.Dir
		if !filepath.IsAbs(workingDir) {
			cwd, _ := os.Getwd()
			workingDir = filepath.Join(cwd, workingDir)
		}
		addTree(workingDir, "rw", true)
	}
	for _, arg := range cmd.Args[1:] {
		path := arg
		if i := strings.IndexByte(path, '='); i > 0 {
			path = path[i+1:]
		}
		if !filepath.IsAbs(path) {
			continue
		}
		if info, err := os.Stat(path); err == nil {
			if info.IsDir() {
				addTree(path, "rw", true)
			} else {
				addPath(path, "ro", "literal", info.Mode().Perm()&0o111 != 0)
			}
		} else {
			addTree(filepath.Dir(path), "rw", true)
		}
	}
	profile.WriteString(rules.String())
	return profile.String()
}
