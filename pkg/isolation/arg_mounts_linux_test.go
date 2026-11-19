//go:build linux

package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsLinuxArgPathCandidate verifies the path-candidate predicate that
// distinguishes plain path tokens from shell command strings.
func TestIsLinuxArgPathCandidate(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want bool
	}{
		// Plain absolute paths: accepted.
		{arg: "/tmp/foo", want: true},
		{arg: "/home/bob/file.txt", want: true},
		{arg: "/opt/toolchain/bin/gcc", want: true},
		{arg: "/tmp/picoclaw_media/voice.silk", want: true},

		// Relative paths: not paths.
		{arg: "relative/path", want: false},
		{arg: "./foo", want: false},
		{arg: "-c", want: false},
		{arg: "--output=", want: false},

		// Shell command strings with metacharacters: rejected.
		{arg: "/home/bob/x; cat /etc/passwd", want: false},
		{arg: "/tmp/a && rm -rf /", want: false},
		{arg: "/tmp/a | tee /tmp/b", want: false},
		{arg: "/tmp/$(cat /etc/passwd)", want: false},
		{arg: "/tmp/`id`", want: false},
		{arg: "/tmp/a > /tmp/b", want: false},
		{arg: "/tmp/a < /tmp/b", want: false},
		{arg: `"/quoted"`, want: false},
		{arg: `'/quoted'`, want: false},
		{arg: "/tmp/back\\slash", want: false},
		{arg: "/tmp/glob*", want: false},
		{arg: "/tmp/glob?", want: false},
		{arg: "/tmp/[ab]", want: false},
		{arg: "/tmp/{a,b}", want: false},

		// Whitespace: rejected.
		{arg: "/tmp/has space", want: false},
		{arg: "/tmp/has\ttab", want: false},
		{arg: "/tmp/has\nnewline", want: false},

		// Empty: rejected.
		{arg: "", want: false},
	} {
		_, got := isLinuxArgPathCandidate(tc.arg)
		if got != tc.want {
			t.Errorf("isLinuxArgPathCandidate(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

// TestIsLinuxBroadMount verifies the too-broad predicate that refuses
// auto-mounting paths that would expose large swaths of the host filesystem.
func TestIsLinuxBroadMount(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}
	homeParent := filepath.Dir(filepath.Clean(home))

	for _, tc := range []struct {
		path string
		want bool
	}{
		// "/" itself.
		{path: "/", want: true},

		// Directly under "/": always too broad.
		{path: "/home", want: true},
		{path: "/etc", want: true},
		{path: "/tmp", want: true},
		{path: "/proc", want: true},
		{path: "/sys", want: true},
		{path: "/dev", want: true},
		{path: "/run", want: true},
		{path: "/var", want: true},
		{path: "/opt", want: true},
		{path: "/usr", want: true},

		// Home directory: too broad.
		{path: home, want: true},

		// Ancestor of home: too broad.
		{path: homeParent, want: true},

		// A specific file inside home: NOT too broad.
		{path: filepath.Join(home, "documents", "file.txt"), want: false},
		{path: filepath.Join(home, "file.txt"), want: false},

		// A path under /tmp/picoclaw_media: NOT too broad (weixin use-case).
		// /tmp/picoclaw_media is at depth 2, not directly under /.
		{path: "/tmp/picoclaw_media", want: false},
		{path: "/tmp/picoclaw_media/weixin-voice-abc123.silk", want: false},
	} {
		got := isLinuxBroadMount(tc.path)
		if got != tc.want {
			t.Errorf("isLinuxBroadMount(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestIsLinuxPseudoFilesystemPath verifies that paths owned by the sandbox's
// own /proc, /sys and /dev trees are never auto-mounted from the host.
func TestIsLinuxPseudoFilesystemPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "/proc", want: true},
		{path: "/proc/cpuinfo", want: true},
		// /proc/self resolves to a host PID that does not exist in the
		// sandbox PID namespace; mounting it breaks bwrap setup.
		{path: "/proc/self/status", want: true},
		{path: "/proc/1/root", want: true},
		{path: "/sys/class/net", want: true},
		{path: "/dev/null", want: true},

		// Names that merely start with the prefix string are not pseudo-fs.
		{path: "/processes", want: false},
		{path: "/sysroot", want: false},
		{path: "/devices", want: false},
		{path: "/tmp/proc/cpuinfo", want: false},
		{path: "/home/bob/proc", want: false},
	} {
		if got := isLinuxPseudoFilesystemPath(tc.path); got != tc.want {
			t.Errorf("isLinuxPseudoFilesystemPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestAppendLinuxArgumentMountsSkipsPseudoFilesystems verifies that argument
// paths under /proc, /sys and /dev do not add bind rules to the mount plan.
func TestAppendLinuxArgumentMountsSkipsPseudoFilesystems(t *testing.T) {
	plan := appendLinuxArgumentMounts(nil, []string{
		"/proc/self/status",
		"/proc/cpuinfo",
		"/sys/class/net",
		"/dev/null",
	})
	if len(plan) != 0 {
		t.Fatalf("pseudo-filesystem arguments added mount rules: %+v", plan)
	}
}

// TestLinuxPlanCoversPath verifies the ancestor-coverage check that prevents
// stacking a conflicting mount mode on top of an existing rule.
func TestLinuxPlanCoversPath(t *testing.T) {
	plan := []MountRule{
		{Source: "/usr", Target: "/usr", Mode: "ro"},
		{Source: "/tmp/picoclaw_media", Target: "/tmp/picoclaw_media", Mode: "rw"},
	}

	for _, tc := range []struct {
		path string
		want bool
	}{
		// Exact match.
		{path: "/usr", want: true},
		{path: "/tmp/picoclaw_media", want: true},

		// Covered by an ancestor rule.
		{path: "/usr/bin/gcc", want: true},
		{path: "/tmp/picoclaw_media/voice.silk", want: true},

		// Not covered.
		{path: "/home/bob/file.txt", want: false},
		{path: "/etc/passwd", want: false},
		{path: "/tmp/other", want: false},
	} {
		got := linuxPlanCoversPath(plan, tc.path)
		if got != tc.want {
			t.Errorf("linuxPlanCoversPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestLinuxArgumentPath_FlagEqualsValue verifies that --flag=value correctly
// extracts the value and applies the path-candidate check.
func TestLinuxArgumentPath_FlagEqualsValue(t *testing.T) {
	// Valid absolute path in --output=...
	dir := t.TempDir()
	arg := "--output=" + filepath.Join(dir, "result.bin")
	got, ok := linuxArgumentPath(arg)
	if !ok {
		t.Fatalf("linuxArgumentPath(%q) = false, want true", arg)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("linuxArgumentPath(%q) = %q, want absolute path", arg, got)
	}

	// Shell metacharacter in the value: rejected.
	bad := "--output=/tmp/a;rm -rf /"
	_, badOK := linuxArgumentPath(bad)
	if badOK {
		t.Fatalf("linuxArgumentPath(%q) must not parse shell metacharacter as path", bad)
	}
}

// TestAppendLinuxArgumentMounts_ShellCmdNotPath verifies that a shell command
// string passed as an argument (the sh -c pattern) never causes any mount.
func TestAppendLinuxArgumentMounts_ShellCmdNotPath(t *testing.T) {
	for _, shell := range []string{
		"/home/bob/x; cat /home/bob/secret.txt",
		"cat /etc/passwd | base64",
		"/tmp/$(whoami)",
	} {
		plan := appendLinuxArgumentMounts(nil, []string{"-c", shell})
		if len(plan) != 0 {
			t.Errorf("shell command %q must not add mounts, got %+v", shell, plan)
		}
	}
}

// TestAppendLinuxArgumentMounts_HomeDirRefused verifies that the home dir and
// a non-existent path whose parent is the home dir are both refused.
func TestAppendLinuxArgumentMounts_HomeDirRefused(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	// Existing home dir as a bare arg.
	plan := appendLinuxArgumentMounts(nil, []string{home})
	if len(plan) != 0 {
		t.Errorf("home dir arg must not add mounts, got %+v", plan)
	}

	// Non-existent path whose parent is home.
	nonexistent := filepath.Join(home, "picoclaw-test-nonexistent-"+t.Name())
	plan = appendLinuxArgumentMounts(nil, []string{nonexistent})
	for _, r := range plan {
		if strings.HasPrefix(filepath.Clean(r.Target), filepath.Clean(home)) {
			t.Errorf("parent of nonexistent home path must not be mounted, got %+v", r)
		}
	}
}

// TestAppendLinuxArgumentMounts_NoCoveringRuleStacking verifies that when the
// plan already has an ancestor rule, no additional (possibly conflicting) rule
// is added for a path covered by that ancestor.
func TestAppendLinuxArgumentMounts_NoCoveringRuleStacking(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-existing rw rule for the parent directory.
	plan := []MountRule{{Source: dir, Target: dir, Mode: "rw"}}
	result := appendLinuxArgumentMounts(plan, []string{file})

	for _, r := range result {
		if filepath.Clean(r.Target) == filepath.Clean(file) {
			t.Fatalf("ro rule must not be stacked on existing rw ancestor, got %+v", r)
		}
	}
	if len(result) != 1 {
		t.Fatalf("plan length = %d, want 1 (no new rules)", len(result))
	}
}

// TestAppendLinuxArgumentMounts_WeixinLegitCase verifies the legitimate Weixin
// SILK transcoder scenario: an existing input file gets an ro mount, and the
// parent of a not-yet-existing output file gets an rw mount.
func TestAppendLinuxArgumentMounts_WeixinLegitCase(t *testing.T) {
	mediaDir := t.TempDir()
	input := filepath.Join(mediaDir, "voice.silk")
	if err := os.WriteFile(input, []byte("silk-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(mediaDir, "voice.wav") // does not exist yet

	// args mirrors silk_v3_decoder call: inputPath, outputPath, sampleRate
	plan := appendLinuxArgumentMounts(nil, []string{input, output, "24000"})

	var hasInputRO, hasParentRW bool
	for _, r := range plan {
		rt := filepath.Clean(r.Target)
		if rt == filepath.Clean(input) && r.Mode == "ro" {
			hasInputRO = true
		}
		if rt == filepath.Clean(mediaDir) && r.Mode == "rw" {
			hasParentRW = true
		}
	}
	if !hasInputRO {
		t.Errorf("existing input file not mounted ro; plan: %+v", plan)
	}
	if !hasParentRW {
		t.Errorf("parent of non-existent output not mounted rw; plan: %+v", plan)
	}
}

// TestAppendLinuxArgumentMounts_SymlinkMountsTarget verifies that a symlink
// argument causes the resolved target to be mounted (not the symlink path
// itself), consistent with bwrap >= 0.12 requirements.
func TestAppendLinuxArgumentMounts_SymlinkMountsTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink("real.txt", link); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxArgumentMounts(nil, []string{link})

	for _, r := range plan {
		if filepath.Clean(r.Target) == filepath.Clean(link) {
			t.Fatalf("symlink path itself must not be mounted, got %+v", r)
		}
	}
	var hasTarget bool
	for _, r := range plan {
		if filepath.Clean(r.Target) == filepath.Clean(target) {
			hasTarget = true
		}
	}
	if !hasTarget {
		t.Errorf("resolved symlink target must be in mount plan; plan: %+v", plan)
	}
}

// TestArgMountsAttackMountPlan_HomeNotExposed is a mount-plan level proof that
// the §6 attack no longer adds the home directory to the plan.
// It does not require bwrap and runs anywhere.
func TestArgMountsAttackMountPlan_HomeNotExposed(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	// Replicate the §6 attack argv: sh -c <shell-string> /home/bob/nonexistent
	shellCmd := "cat " + filepath.Join(home, "secret.txt") + "; echo x > " +
		filepath.Join(home, "arg-parent-write") + "; echo rc=$?"
	nonexistent := filepath.Join(home, "nonexistent-target")
	args := []string{"-c", shellCmd, nonexistent}

	plan := appendLinuxArgumentMounts(nil, args)

	for _, r := range plan {
		rt := filepath.Clean(r.Target)
		if rt == home {
			t.Errorf("home dir must not appear in plan; got %+v", r)
		}
		if rt == filepath.Clean(nonexistent) {
			t.Errorf("nonexistent home path must not appear in plan; got %+v", r)
		}
	}
	if len(plan) != 0 {
		t.Logf("plan after attack args (should be empty or harmless): %+v", plan)
	}
}

// TestArgMountsLiveBwrap_SecretNotReadable runs a real bwrap sandbox and
// verifies that the secret file inside the home dir is not reachable when the
// attack argv from §6 is used. Skipped when bwrap is unavailable.
func TestArgMountsLiveBwrap_SecretNotReadable(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not found; skipping live sandbox test")
	}
	probe := exec.Command(bwrapPath,
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "true")
	if probeErr := probe.Run(); probeErr != nil {
		t.Skipf("bwrap namespace creation failed (%v); skipping live sandbox test", probeErr)
	}

	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skip("cannot determine home dir")
	}

	secretFile := filepath.Join(home, "picoclaw-bwrap-test-secret-"+t.Name())
	if err := os.WriteFile(secretFile, []byte("SHOULD_NOT_BE_READ"), 0o600); err != nil {
		t.Skipf("cannot create test secret file: %v", err)
	}
	defer os.Remove(secretFile)

	root := t.TempDir()
	// Build a mount plan from just the attack args; the secret file must not
	// appear in it after our fix.
	nonexistent := filepath.Join(home, "picoclaw-bwrap-test-nonexistent-"+t.Name())
	shellCmd := "cat " + secretFile + " 2>/dev/null && echo SECRET_READ; echo OK"
	plan := appendLinuxArgumentMounts(
		BuildLinuxMountPlan(root, nil),
		[]string{"-c", shellCmd, nonexistent},
	)

	for _, r := range plan {
		rt := filepath.Clean(r.Target)
		if rt == home || rt == secretFile {
			t.Errorf("mount plan contains home/secret after fix: %+v", r)
		}
	}

	// Also verify via bwrap: sh must not be able to read the secret.
	namespaceFlags, nsErr := cachedLinuxNamespaceFlags(bwrapPath)
	if nsErr != nil {
		t.Skipf("namespace probe failed: %v", nsErr)
	}
	bwrapArgs, buildErr := buildLinuxBwrapArgs(
		"sh", "sh",
		[]string{"sh", "-c", shellCmd, nonexistent},
		"",
		plan,
		namespaceFlags,
	)
	if buildErr != nil {
		t.Fatalf("buildLinuxBwrapArgs: %v", buildErr)
	}

	cmd := exec.Command(bwrapPath, bwrapArgs[1:]...)
	out, runErr := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if strings.Contains(outStr, "SECRET_READ") {
		t.Errorf("secret was readable inside sandbox; bwrap output: %s", outStr)
	}
	if runErr != nil {
		t.Logf("bwrap exit (expected fail-closed): %v; output: %s", runErr, outStr)
	}
}
