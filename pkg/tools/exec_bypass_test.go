package tools

import (
	"runtime"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newHardlineTestTool isolates the hardline floor: the optional regex and risk
// layers are disabled, and the legacy allow_unknown switch is on. Anything the
// tool still rejects is a hardline decision.
func newHardlineTestTool(t *testing.T, restrict bool) *ExecTool {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Tools.Exec.EnableDenyPatterns = false
	cfg.Tools.Exec.AllowUnknown = true
	tool, err := NewExecToolWithConfig(t.TempDir(), restrict, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	return tool
}

func TestExecStrictUnknownBlocksOpaquePrograms(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	tool := riskTestTool(t, config.ExecConfig{})
	commands := []string{
		"sh script.sh",
		"bash scripts/deploy.sh",
		"python3 script.py",
		"python3 -",
		"python3 -m http.server",
		"python3 -c \"$CODE\"",
		"perl script.pl",
		"node app.js",
		"ruby app.rb",
		"php app.php",
		"lua script.lua",
		"Rscript script.R",
		"osascript script.scpt",
		"awk -f script.awk",
		"sed -f script.sed",
		"python3 - <<'EOF'\nprint('payload')\nEOF",
		"perl - <<'EOF'\nunlink '/tmp/canary'\nEOF",
		"node - <<'EOF'\nconsole.log('payload')\nEOF",
		"env -S 'python3 script.py'",
		"xargs python3 script.py",
		`find . -exec sh script.sh \;`,
		`trap 'python3 script.py' EXIT`,
		`eval 'python3 script.py'`,
	}
	for _, command := range commands {
		got := tool.guardCommand(command, t.TempDir())
		if !strings.Contains(got, "strict unknown policy") {
			t.Errorf("opaque command %q should be blocked by strict policy, got %q", command, got)
		}
	}
}

func TestExecAllowUnknownRestoresLegacyOpaqueBehavior(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	tool := riskTestTool(t, config.ExecConfig{AllowUnknown: true})
	for _, command := range []string{
		"sh script.sh",
		"python3 script.py",
		"node app.js",
		"python3 -",
	} {
		if got := tool.guardCommand(command, t.TempDir()); got != "" {
			t.Errorf("allow_unknown should allow opaque command %q, got %q", command, got)
		}
	}
}

func TestExecHardlineASTBlocksProtectedOperations(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	tool := newHardlineTestTool(t, false)
	commands := []string{
		"rm -rf /",
		"rm -r -f /",
		"rm -rf -- /",
		"rm -rf /*",
		"rm -rf //",
		"rm -rf /tmp/..",
		"rm -rf /etc",
		"rm -rf /usr/",
		"rm -rf /home/*",
		"find / -maxdepth 1 -delete",
		"find /etc -name '*.conf' -delete",
		"chmod -R 000 /",
		"chown -R nobody /",
		"chgrp -R nogroup /etc",
		"dd of=/dev/sda",
		"dd if=/dev/sda of=/tmp/image",
		"echo x > /dev/sda",
		"kill -1",
		"kill -- -1",
		"kill -9 1",
		"kill -s KILL 1",
		"kill -KILL 1",
		"sh -c 'rm -rf /'",
		"env sh -c 'rm -rf /'",
		"eval 'rm -rf /'",
		"trap 'rm -rf /' EXIT",
		"bomb(){ bomb|bomb& };bomb",
		"f(){ f|f& };f",
		"fanout(){ fanout& fanout& };fanout",
		":(){ :|:& };:",
		"busybox rm -rf /",
		"env rm -rf /",
		"find / -exec rm -rf {} +",
		"sh -c 'env rm -rf /etc'",
	}
	for _, command := range commands {
		got := tool.guardCommand(command, t.TempDir())
		if !strings.Contains(got, "safety guard") {
			t.Errorf("hardline command %q should be blocked, got %q", command, got)
		}
	}
}

func TestExecHardlineASTLeavesWorkspaceDeletesToRisk(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	tool := newHardlineTestTool(t, true)
	for _, command := range []string{
		"rm -rf ./build",
		"rm -f ./build/file",
		"find . -delete",
		"chmod -R 000 ./build",
	} {
		if got := tool.guardCommand(command, t.TempDir()); got != "" {
			t.Errorf("workspace-local command %q should not match hardline, got %q", command, got)
		}
	}
}

// TestExecHardlineNeverAllowsProtectedRoot is the property-style guard for the
// hardline floor: every generated destructive form targeting a protected root
// must be rejected regardless of quoting or command spelling.
func TestExecHardlineNeverAllowsProtectedRoot(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	tool := newHardlineTestTool(t, false)
	roots := []string{"/", "/etc", "/usr", "/home", "/root", "/var", "/bin", "/proc", "/*"}
	templates := []string{
		"rm -rf %s",
		"rm -r -f %s",
		"rm --recursive --force %s",
		"find %s -delete",
		"chmod -R 000 %s",
		"chown -R nobody %s",
	}
	for _, root := range roots {
		for _, template := range templates {
			command := strings.ReplaceAll(template, "%s", root)
			if got := tool.guardCommand(command, t.TempDir()); !strings.Contains(got, "safety guard") {
				t.Errorf("protected-root command %q must be blocked, got %q", command, got)
			}
		}
	}
}

func TestExecParseFailureIsNeverAllowed(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	for _, allowUnknown := range []bool{false, true} {
		cfg := config.DefaultConfig()
		cfg.Tools.Exec.AllowUnknown = allowUnknown
		tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
		if err != nil {
			t.Fatalf("NewExecToolWithConfig: %v", err)
		}
		got := tool.guardCommand("echo )", t.TempDir())
		if !strings.Contains(got, "could not be parsed") {
			t.Errorf("parse failure with allow_unknown=%v should be blocked, got %q", allowUnknown, got)
		}
	}
}

func FuzzExecPolicyNeverAllowsHardlineFinding(f *testing.F) {
	seeds := []string{
		"rm -rf /",
		"find / -delete",
		`find / -exec rm -rf {} +`,
		"bomb(){ bomb|bomb& };bomb",
		"chmod -R 000 /",
		"dd of=/dev/sda",
		"kill -9 1",
		"echo hi",
		"python3 script.py",
		"search=rm; $search -rf /",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.EnableDenyPatterns = false
	cfg.Tools.Exec.AllowUnknown = true
	tool, err := NewExecToolWithConfig("/workspace", false, cfg)
	if err != nil {
		f.Fatalf("NewExecToolWithConfig: %v", err)
	}
	f.Fuzz(func(t *testing.T, command string) {
		analysis := shellguard.Analyze(command)
		finding, found := shellguard.HardlineFile(analysis.File, "/workspace", "/home/user")
		if !found {
			return
		}
		decision := tool.guardCommandContext(t.Context(), command, "/workspace")
		if decision.Action != shellguard.ActionBlock {
			t.Fatalf("hardline finding %q was not blocked: action=%s rule=%q",
				finding.Rule, decision.Action, decision.Rule)
		}
	})
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST guard semantics")
	}
}
