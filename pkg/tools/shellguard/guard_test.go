package shellguard

import (
	"strings"
	"testing"
)

func mustGuard(t *testing.T, threshold string) *Guard {
	t.Helper()
	guard, err := NewGuard(threshold, nil, nil)
	if err != nil {
		t.Fatalf("NewGuard(%q): %v", threshold, err)
	}
	return guard
}

func TestGuardCheck(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "medium")

	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{name: "simple allow", command: "ls -la"},
		{name: "pipeline allow", command: "cat file | grep needle | wc -l"},
		{name: "conditional allow", command: "test -f file && echo yes || echo no"},
		{name: "loop allow", command: `for f in *.go; do cat "$f"; done`},
		{name: "quoted command allow", command: `"echo" hello`},
		{name: "redirection allow", command: "echo hello > output.txt"},
		{name: "rm blocked", command: "rm -rf /", blocked: true},
		{name: "quoted rm blocked", command: `"r"'m' -rf /`, blocked: true},
		{name: "ansi-c quoted rm blocked", command: `$'\x72m' -rf /`, blocked: true},
		{name: "variable indirection blocked", command: "x=rm; $x -rf /", blocked: true},
		{name: "command substitution blocked", command: "echo $(rm -rf /)", blocked: true},
		{name: "assignment substitution blocked", command: "x=$(rm -rf /); echo ok", blocked: true},
		{name: "sudo blocked", command: "sudo ls", blocked: true},
		{name: "eval blocked", command: "eval ls", blocked: true},
		{name: "find delete blocked", command: "find . -delete", blocked: true},
		{name: "chmod blocked", command: "chmod +x script.sh", blocked: true},
		{name: "parse error blocked", command: "echo )", blocked: true},
		{name: "trap risky action blocked", command: "trap 'rm -rf /' EXIT", blocked: true},
		{name: "trap safe action allowed", command: "trap 'echo bye' EXIT"},
		{name: "trap reset allowed", command: "trap - INT"},
		{name: "trap dynamic action blocked", command: `trap "$ACTION" EXIT`, blocked: true},
		{name: "wrapped trap risky action blocked", command: "env trap 'rm -rf /' EXIT", blocked: true},
		{name: "shell script file blocked", command: "sh script.sh", blocked: true},
		{name: "shell stdin blocked", command: "echo rm | sh", blocked: true},
		{name: "shell literal safe script allowed", command: "sh -c 'exit 7'"},
		{name: "shell literal risky script blocked", command: "sh -c 'rm -rf /'", blocked: true},
		{name: "shell dynamic script blocked", command: `sh -c "$SCRIPT"`, blocked: true},
		{name: "shell combined flags safe", command: "sh -ec 'echo ok'"},
		{name: "env safe command allowed", command: "env FOO=bar ls"},
		{name: "env risky command blocked", command: "env rm -rf /", blocked: true},
		{name: "env split string blocked", command: "env -S 'rm -rf /'", blocked: true},
		{name: "env attached split string blocked", command: "env -S'rm -rf /'", blocked: true},
		{name: "env combined split string blocked", command: "env -iS'rm -rf /'", blocked: true},
		{name: "env attached unset allowed", command: "env -uFOO echo ok"},
		{name: "env attached unset S allowed", command: "env -uS echo ok"},
		{name: "env option-like argument allowed", command: "env FOO=bar echo -S"},
		{name: "xargs safe command allowed", command: "xargs echo"},
		{name: "xargs rm base allowed", command: "xargs rm"},
		{name: "xargs risky command blocked", command: "xargs rm -rf /", blocked: true},
		{name: "xargs shell blocked", command: "xargs sh -c 'echo ok'", blocked: true},
		{name: "timeout safe command allowed", command: "timeout 5 echo ok"},
		{name: "timeout risky command blocked", command: "timeout 5 rm -rf /", blocked: true},
		{name: "time output flag keeps command", command: "/usr/bin/time -o /tmp/timing rm -rf /", blocked: true},
		{name: "setsid risky command blocked", command: "setsid rm -rf /", blocked: true},
		{name: "setsid safe command allowed", command: "setsid echo ok"},
		{name: "command lookup allowed", command: "command -v rm"},
		{name: "command combined lookup allowed", command: "command -pv rm"},
		{name: "command execution blocked", command: "command rm -rf /", blocked: true},
		{name: "docker ps allowed", command: "docker ps"},
		{name: "docker logs allowed", command: "docker logs app"},
		{name: "docker inspect allowed", command: "docker inspect app"},
		{name: "docker run blocked", command: "docker run image", blocked: true},
		{name: "systemctl status allowed", command: "systemctl status nginx"},
		{name: "systemctl start blocked", command: "systemctl start nginx", blocked: true},
		{name: "systemctl poweroff blocked", command: "systemctl poweroff", blocked: true},
		{name: "kubectl get allowed", command: "kubectl get pods"},
		{name: "kubectl describe allowed", command: "kubectl describe pod x"},
		{name: "kubectl delete blocked", command: "kubectl delete pod x", blocked: true},
		{name: "git status allowed", command: "git status"},
		{name: "git branch delete blocked", command: "git branch -D feature", blocked: true},
		{name: "git force-with-lease blocked", command: "git push --force-with-lease origin", blocked: true},
		{name: "rm base allowed", command: "rm file"},
		{name: "rm recursive blocked", command: "rm -r build", blocked: true},
		{name: "rm recursive force blocked", command: "rm -rf build", blocked: true},
		{name: "wipefs blocked", command: "wipefs /dev/sda", blocked: true},
		{name: "blkdiscard blocked", command: "blkdiscard /dev/sda", blocked: true},
		{name: "cryptsetup status allowed", command: "cryptsetup status /dev/sda"},
		{name: "cryptsetup format blocked", command: "cryptsetup luksFormat /dev/sda", blocked: true},
		{name: "zpool status allowed", command: "zpool status"},
		{name: "zpool destroy blocked", command: "zpool destroy tank", blocked: true},
		{name: "mdadm detail allowed", command: "mdadm --detail /dev/md0"},
		{name: "mdadm zero superblock blocked", command: "mdadm --zero-superblock /dev/sda", blocked: true},
		{name: "terraform plan allowed", command: "terraform plan"},
		{name: "terraform destroy blocked", command: "terraform destroy", blocked: true},
		{name: "helm list allowed", command: "helm list"},
		{name: "helm uninstall blocked", command: "helm uninstall release", blocked: true},
		{name: "aws s3 ls allowed", command: "aws s3 ls"},
		{name: "aws s3 rm blocked", command: "aws s3 rm s3://bucket/key", blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			blocked := guard.Check(tt.command)
			if tt.blocked && blocked == nil {
				t.Fatalf("Check(%q) allowed, want blocked", tt.command)
			}
			if !tt.blocked && blocked != nil {
				t.Fatalf("Check(%q) blocked: %v", tt.command, blocked)
			}
			if blocked != nil && !strings.Contains(blocked.Error(), "risk_level=") {
				t.Fatalf("blocked error lacks structured fields: %v", blocked)
			}
		})
	}
}

func TestGuardThreshold(t *testing.T) {
	t.Parallel()

	low := mustGuard(t, "low")
	if blocked := low.Check("cp a b"); blocked == nil {
		t.Fatal("medium command should be blocked at low threshold")
	}
	if blocked := low.Check("cat a"); blocked != nil {
		t.Fatalf("low command should pass at low threshold: %v", blocked)
	}

	critical := mustGuard(t, "critical")
	if blocked := critical.Check("rm -rf /"); blocked != nil {
		t.Fatalf("critical threshold should allow critical commands: %v", blocked)
	}
}

func TestGuardOverridesAndModifiers(t *testing.T) {
	t.Parallel()

	guard, err := NewGuard(
		"medium",
		map[string]string{"chmod": "low"},
		map[string][]ArgModifier{
			"echo": {{Args: []string{"--danger"}, Level: RiskHigh}},
		},
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}

	if blocked := guard.Check("chmod +x script.sh"); blocked != nil {
		t.Fatalf("override should allow chmod: %v", blocked)
	}
	if blocked := guard.Check("echo --danger"); blocked == nil {
		t.Fatal("user modifier should block echo --danger")
	}
}

func TestGuardRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	if _, err := NewGuard("bogus", nil, nil); err == nil {
		t.Fatal("invalid threshold should fail")
	}
	if _, err := NewGuard("medium", map[string]string{"rm": "bogus"}, nil); err == nil {
		t.Fatal("invalid override should fail")
	}
	if _, err := NewGuard("medium", nil, map[string][]ArgModifier{
		"echo": {{Args: []string{"x"}, Level: RiskLevel(99)}},
	}); err == nil {
		t.Fatal("invalid modifier level should fail")
	}
	if _, err := NewGuard("medium", nil, map[string][]ArgModifier{
		"echo": {{Level: RiskHigh}},
	}); err == nil {
		t.Fatal("empty modifier args should fail")
	}
}

func TestGuardNestingLimit(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "medium")
	if blocked := guard.checkScript("echo ok", maxShellNesting+1); blocked == nil {
		t.Fatal("depth beyond the nesting limit should be blocked")
	}
	if blocked := guard.checkScript("echo ok", maxShellNesting); blocked != nil {
		t.Fatalf("depth at the nesting limit should pass: %v", blocked)
	}
}

func FuzzGuardCheck(f *testing.F) {
	for _, seed := range []string{
		"ls -la",
		"rm -rf /",
		"x=rm; $x -rf /",
		"sh -c 'echo ok'",
		"env FOO=bar ls",
		"find . -delete",
	} {
		f.Add(seed)
	}
	guard, err := NewGuard("medium", nil, nil)
	if err != nil {
		f.Fatalf("NewGuard: %v", err)
	}
	f.Fuzz(func(t *testing.T, command string) {
		// The guard must never panic, regardless of input shape.
		_ = guard.Check(command)
	})
}

func BenchmarkGuardCheck(b *testing.B) {
	guard, err := NewGuard("medium", nil, nil)
	if err != nil {
		b.Fatalf("NewGuard: %v", err)
	}

	commands := map[string]string{
		"simple":   "go test ./pkg/tools/... -run TestShellTool -count=1",
		"pipeline": "cat file | grep needle | wc -l",
		"nested":   "sh -c 'echo $(date) | grep ok'",
	}
	for name, command := range commands {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = guard.Check(command)
			}
		})
	}
}
