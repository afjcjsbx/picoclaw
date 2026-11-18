package tools

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// newHardlineTestTool isolates the hardline floor: the optional regex and risk
// layers are disabled, and the legacy allow_unknown switch is on. Anything the
// tool still rejects is a hardline decision.
func newHardlineTestTool(t *testing.T, restrict bool) *ExecTool {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
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
		":(){ :|:& };:",
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
