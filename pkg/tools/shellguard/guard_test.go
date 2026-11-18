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

// checkGuard applies the strict test policy: unknown commands are rejected.
// Production callers use AssessFile and choose how to treat unknown commands.
func checkGuard(g *Guard, command string) *BlockedError {
	if g == nil {
		return nil
	}
	file, _ := Parse(command)
	assessment := g.AssessFile(command, file)
	if assessment.Reject || assessment.Unknown ||
		!IsAllowed(assessment.Level, g.Threshold()) {
		return assessment.BlockedError(g.Threshold())
	}
	return nil
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
		{name: "env split after unset blocked", command: "env -u NAME -S 'rm -rf /'", blocked: true},
		{name: "env split after chdir blocked", command: "env -C /tmp -S 'rm -rf /'", blocked: true},
		{name: "env split after unset long blocked", command: "env -u NAME --split-string='rm -rf /'", blocked: true},
		{name: "env split after attached unset blocked", command: "env -uNAME -S 'rm -rf /'", blocked: true},
		{name: "env split after ignore blocked", command: "env -i -S 'rm -rf /'", blocked: true},
		{name: "env unset value is not split", command: "env -u NAME -C /tmp echo ok"},
		{name: "xargs safe command allowed", command: "xargs echo"},
		{name: "xargs rm base allowed", command: "xargs rm"},
		{name: "xargs risky command blocked", command: "xargs rm -rf /", blocked: true},
		{name: "xargs shell blocked", command: "xargs sh -c 'echo ok'", blocked: true},
		{name: "busybox safe applet allowed", command: "busybox ls -la"},
		{name: "busybox risky applet blocked", command: "busybox rm -rf /", blocked: true},
		{name: "busybox shell script blocked", command: "busybox sh -c 'rm -rf /'", blocked: true},
		{name: "timeout safe command allowed", command: "timeout 5 echo ok"},
		{name: "timeout risky command blocked", command: "timeout 5 rm -rf /", blocked: true},
		{name: "time output flag keeps command", command: "/usr/bin/time -o /tmp/timing rm -rf /", blocked: true},
		{name: "setsid risky command blocked", command: "setsid rm -rf /", blocked: true},
		{name: "setsid detached command blocked", command: "setsid echo ok", blocked: true},
		{name: "command lookup allowed", command: "command -v rm"},
		{name: "command combined lookup allowed", command: "command -pv rm"},
		{name: "command execution blocked", command: "command rm -rf /", blocked: true},
		{name: "command nested verbose flag blocked", command: "command rm -rf -v build", blocked: true},
		{name: "command trailing verbose flag blocked", command: "command chmod -v 777 script.sh", blocked: true},
		{name: "command verbose ssh blocked", command: "command ssh -v host", blocked: true},
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
			blocked := checkGuard(guard, tt.command)
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

func TestGuardRejectsDynamicDestructiveOperands(t *testing.T) {
	t.Parallel()

	for _, threshold := range []string{"medium", "critical"} {
		guard := mustGuard(t, threshold)
		blocked := []string{
			`rm -f "$P"`,
			`rm -f $P`,
			`rm -rf "$P"`,
			`r\m -rf "$P"`,
			`r\m -f "$P"`,
			`env rm -f "$P"`,
			`command rm -f "$P"`,
			`sh -c 'rm -f "$P"'`,
			`eval 'rm -f "$P"'`,
			`trap 'rm -f "$P"' EXIT`,
			`find . -exec rm -f "$P" \;`,
			`xargs rm -f "$P"`,
			`cp "$SRC" dest`,
			`chmod 600 "$P"`,
		}
		for _, command := range blocked {
			err := checkGuard(guard, command)
			if err == nil {
				t.Errorf("threshold %s: Check(%q) allowed, want rejected", threshold, command)
				continue
			}
			if !strings.Contains(err.Error(), "dynamic operand for destructive command") {
				t.Errorf("threshold %s: Check(%q) = %v, want dynamic-operand rejection", threshold, command, err)
			}
		}

		allowed := []string{
			`rm -f build`,
			`cat "$P"`,
			`echo "$X"`,
			`git status`,
		}
		for _, command := range allowed {
			if err := checkGuard(guard, command); err != nil {
				t.Errorf("threshold %s: Check(%q) blocked: %v", threshold, command, err)
			}
		}
	}
}

func TestParseEnvArgs(t *testing.T) {
	t.Parallel()

	words := func(texts ...string) []word {
		result := make([]word, len(texts))
		for i, text := range texts {
			result[i] = word{text: text}
		}
		return result
	}

	tests := []struct {
		name       string
		args       []word
		command    []string
		chdir      string
		hasChdir   bool
		unset      []string
		split      bool
		splitValue string
	}{
		{
			name:    "assignments and command",
			args:    words("FOO=bar", "echo", "ok"),
			command: []string{"echo", "ok"},
		},
		{
			name:    "unset value is consumed",
			args:    words("-u", "NAME", "echo", "ok"),
			command: []string{"echo", "ok"},
			unset:   []string{"NAME"},
		},
		{
			name:     "chdir value is consumed",
			args:     words("-C", "/tmp", "echo", "ok"),
			command:  []string{"echo", "ok"},
			chdir:    "/tmp",
			hasChdir: true,
		},
		{
			name:       "split after value option",
			args:       words("-u", "NAME", "-S", "rm -rf /"),
			unset:      []string{"NAME"},
			split:      true,
			splitValue: "rm -rf /",
		},
		{
			name:       "short cluster split",
			args:       words("-iS", "rm -rf /"),
			split:      true,
			splitValue: "rm -rf /",
		},
		{
			name:    "double dash stops options",
			args:    words("--", "echo", "-S"),
			command: []string{"echo", "-S"},
		},
		{
			name:    "assignment after double dash",
			args:    words("--", "FOO=bar", "echo"),
			command: []string{"echo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseEnvArgs(tt.args)
			var command []string
			for _, w := range got.command {
				command = append(command, w.text)
			}
			if len(command) != len(tt.command) {
				t.Fatalf("command = %v, want %v", command, tt.command)
			}
			for i := range command {
				if command[i] != tt.command[i] {
					t.Fatalf("command = %v, want %v", command, tt.command)
				}
			}
			if got.chdir.text != tt.chdir || got.hasChdir != tt.hasChdir {
				t.Fatalf("chdir = %q/%v, want %q/%v", got.chdir.text, got.hasChdir, tt.chdir, tt.hasChdir)
			}
			if len(got.unset) != len(tt.unset) {
				t.Fatalf("unset = %v, want %v", got.unset, tt.unset)
			}
			for i := range got.unset {
				if got.unset[i] != tt.unset[i] {
					t.Fatalf("unset = %v, want %v", got.unset, tt.unset)
				}
			}
			if got.splitString != tt.split || got.splitValue.text != tt.splitValue {
				t.Fatalf("split = %v/%q, want %v/%q", got.splitString, got.splitValue.text, tt.split, tt.splitValue)
			}
		})
	}
}

func TestGuardFindExecNestedCommands(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "high")
	blocked := []string{
		`find . -exec rm -rf build \;`,
		`find . -execdir rm -rf build +`,
		`find . -ok rm -rf build \;`,
		`find . -exec sh -c 'rm -rf build' \;`,
	}
	for _, command := range blocked {
		if got := checkGuard(guard, command); got == nil {
			t.Errorf("Check(%q) allowed, want nested command blocked", command)
		}
	}

	allowed := []string{
		`find . -exec echo ok \;`,
		`find . -exec chmod +x script.sh \;`,
	}
	for _, command := range allowed {
		if got := checkGuard(guard, command); got != nil {
			t.Errorf("Check(%q) blocked: %v", command, got)
		}
	}
}

func TestGuardRejectsParseFailure(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "medium")
	file, err := Parse("echo )")
	if err == nil {
		t.Fatal("Parse accepted an unparseable command")
	}
	if file != nil {
		t.Fatalf("Parse returned a partial tree with the error: %#v", file)
	}
	assessment := guard.AssessFile("echo )", file)
	if !assessment.Unknown {
		t.Fatalf("AssessFile did not mark the parse failure unknown: %#v", assessment)
	}
	if blocked := checkGuard(guard, "echo )"); blocked == nil {
		t.Fatal("strict policy allowed a parse failure")
	}
}

func TestGuardThreshold(t *testing.T) {
	t.Parallel()

	low := mustGuard(t, "low")
	if blocked := checkGuard(low, "cp a b"); blocked == nil {
		t.Fatal("medium command should be blocked at low threshold")
	}
	if blocked := checkGuard(low, "cat a"); blocked != nil {
		t.Fatalf("low command should pass at low threshold: %v", blocked)
	}

	critical := mustGuard(t, "critical")
	if blocked := checkGuard(critical, "rm -rf /"); blocked != nil {
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

	if blocked := checkGuard(guard, "chmod +x script.sh"); blocked != nil {
		t.Fatalf("override should allow chmod: %v", blocked)
	}
	if blocked := checkGuard(guard, "echo --danger"); blocked == nil {
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

func TestGuardWrapperInspection(t *testing.T) {
	t.Parallel()

	medium := mustGuard(t, "medium")
	high := mustGuard(t, "high")
	critical := mustGuard(t, "critical")

	allowed := []struct {
		guard   *Guard
		command string
	}{
		{medium, "flock /tmp/lock echo ok"},
		{medium, "flock /tmp/lock -c 'echo ok'"},
		{medium, "taskset -c 0-3 echo ok"},
		{medium, "chrt -f 50 echo ok"},
		{medium, "busybox echo ok"},
		{medium, "busybox ls -la"},
		{medium, "lxc list"},
		{medium, "incus list"},
		{medium, "machinectl list"},
		{medium, "virsh list"},
		{medium, "virsh domstate vm"},
		{high, "setpriv --reuid 1000 echo ok"},
		{high, "capsh --print"},
		{high, "capsh -- -c 'echo ok'"},
		{high, "lxc exec c1 echo ok"},
		{high, "lxc exec c1 -- echo ok"},
		{high, "lxc exec -- c1 echo ok"},
		{high, "lxc exec --env FOO=bar c1 echo ok"},
		{high, "lxc exec c1 --env FOO=bar echo ok"},
		{high, "machinectl shell user@m echo ok"},
		{high, "machinectl -- shell user@m echo ok"},
		{medium, "timeout -- 5 echo ok"},
		{medium, "flock -- /tmp/lock echo ok"},
		{critical, "runuser -c 'echo ok'"},
	}
	for _, tt := range allowed {
		if blocked := checkGuard(tt.guard, tt.command); blocked != nil {
			t.Errorf("Check(%q) blocked: %v", tt.command, blocked)
		}
	}

	blockedCommands := []struct {
		guard   *Guard
		command string
	}{
		{medium, "flock /tmp/lock rm -rf /"},
		{medium, "flock /tmp/lock -c 'rm -rf /'"},
		{medium, "taskset -c 0-3 rm -rf /"},
		{medium, "chrt -f 50 rm -rf /"},
		{medium, "busybox rm -rf /"},
		{medium, "busybox sh -c 'rm -rf /'"},
		{medium, "busybox --install -s"},
		{medium, "lxc delete c1"},
		{medium, "incus delete c1"},
		{medium, "machinectl poweroff"},
		{medium, "virsh destroy vm"},
		{medium, "virsh qemu-monitor-command vm cmd"},
		{high, "setpriv --reuid 1000 rm -rf /"},
		{high, "capsh -- -c 'rm -rf /'"},
		{high, "lxc exec c1 sh -c 'rm -rf /'"},
		{high, "lxc exec --env FOO=bar c1 rm -rf /"},
		{high, "lxc exec c1 --env FOO=bar rm -rf /"},
		{high, "lxc exec -- c1 rm -rf /"},
		{high, "machinectl shell user@m sh -c 'rm -rf /'"},
		{high, "machinectl -- shell user@m rm -rf /"},
		{medium, "timeout -- 5 rm -rf /"},
		{medium, "flock -- /tmp/lock rm -rf /"},
		{critical, "runuser -c 'x=rm; $x -rf /'"},
	}
	for _, tt := range blockedCommands {
		if blocked := checkGuard(tt.guard, tt.command); blocked == nil {
			t.Errorf("Check(%q) allowed, want blocked", tt.command)
		}
	}
}

func TestGuardWithThreshold(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "medium")
	high := guard.WithThreshold(RiskHigh)
	if high == nil {
		t.Fatal("WithThreshold returned nil for a non-nil guard")
	}

	if blocked := checkGuard(guard, "chmod +x script.sh"); blocked == nil {
		t.Fatal("medium guard should block high risk")
	}
	if blocked := checkGuard(high, "chmod +x script.sh"); blocked != nil {
		t.Fatalf("high guard should allow high risk: %v", blocked)
	}
	if blocked := checkGuard(high, "rm -rf build"); blocked == nil {
		t.Fatal("high guard should still block critical risk")
	}
	if blocked := checkGuard(guard, "chmod +x script.sh"); blocked == nil {
		t.Fatal("WithThreshold must not mutate the original guard")
	}
}

func TestGuardNestingLimit(t *testing.T) {
	t.Parallel()

	guard := mustGuard(t, "medium")
	if assessment := guard.assessScript("echo ok", maxShellNesting+1); !assessment.Unknown {
		t.Fatal("depth beyond the nesting limit should be unknown")
	}
	if assessment := guard.assessScript("echo ok", maxShellNesting); assessment.Unknown {
		t.Fatalf("depth at the nesting limit should pass: %+v", assessment)
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
		_ = checkGuard(guard, command)
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
				_ = checkGuard(guard, command)
			}
		})
	}
}
