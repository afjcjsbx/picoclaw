package shellguard

import (
	"runtime"
	"testing"
)

func TestParseRiskLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    RiskLevel
		wantErr bool
	}{
		{in: "low", want: RiskLow},
		{in: "medium", want: RiskMedium},
		{in: "HIGH", want: RiskHigh},
		{in: " critical ", want: RiskCritical},
		{in: "", want: RiskMedium},
		{in: "off", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParseRiskLevel(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseRiskLevel(%q) = %v, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRiskLevel(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseRiskLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestClassifyCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want RiskLevel
	}{
		{name: "known low", args: []string{"ls", "-la"}, want: RiskLow},
		{name: "known medium", args: []string{"cp", "a", "b"}, want: RiskMedium},
		{name: "rm base", args: []string{"rm", "file"}, want: RiskMedium},
		{name: "known critical", args: []string{"sudo", "ls"}, want: RiskCritical},
		{name: "unknown defaults to medium", args: []string{"some-new-tool"}, want: RiskMedium},
		{name: "empty defaults to medium", args: nil, want: RiskMedium},
		{name: "path basename", args: []string{"/bin/rm", "file"}, want: RiskMedium},
		{name: "rm recursive force", args: []string{"rm", "-rf", "build"}, want: RiskCritical},
		{name: "rm force recursive", args: []string{"rm", "-fr", "build"}, want: RiskCritical},
		{name: "rm uppercase recursive force", args: []string{"rm", "-Rf", "build"}, want: RiskCritical},
		{name: "rm split flags", args: []string{"rm", "-r", "-f", "build"}, want: RiskCritical},
		{name: "rm long flags", args: []string{"rm", "--recursive", "--force", "build"}, want: RiskCritical},
		{name: "git push", args: []string{"git", "push", "origin", "main"}, want: RiskHigh},
		{name: "git push force", args: []string{"git", "push", "--force", "origin"}, want: RiskCritical},
		{name: "git status", args: []string{"git", "status"}, want: RiskLow},
		{name: "curl get", args: []string{"curl", "https://example.com"}, want: RiskMedium},
		{name: "curl post", args: []string{"curl", "-X", "POST", "https://example.com"}, want: RiskHigh},
		{name: "curl attached post", args: []string{"curl", "-XPOST", "https://example.com"}, want: RiskHigh},
		{name: "curl attached data", args: []string{"curl", "-d@file", "https://example.com"}, want: RiskHigh},
		{name: "curl long equals", args: []string{"curl", "--request=POST", "https://example.com"}, want: RiskHigh},
		{name: "git log grep is not push", args: []string{"git", "log", "--grep=push"}, want: RiskLow},
		{name: "find delete", args: []string{"find", ".", "-delete"}, want: RiskHigh},
		{name: "find exec", args: []string{"find", ".", "-exec", "rm", "{}", ";"}, want: RiskHigh},
		{name: "kill term", args: []string{"kill", "1234"}, want: RiskHigh},
		{name: "kill sigkill", args: []string{"kill", "-9", "1234"}, want: RiskCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyCommand(tt.args, nil); got != tt.want {
				t.Fatalf("ClassifyCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestClassifySubcommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want RiskLevel
	}{
		{name: "docker ps", args: []string{"docker", "ps"}, want: RiskLow},
		{name: "docker logs", args: []string{"docker", "logs", "app"}, want: RiskLow},
		{name: "docker inspect", args: []string{"docker", "inspect", "app"}, want: RiskLow},
		{name: "docker host flag then ps", args: []string{"docker", "--host", "unix:///x", "ps"}, want: RiskLow},
		{name: "docker pull", args: []string{"docker", "pull", "image"}, want: RiskMedium},
		{name: "docker run", args: []string{"docker", "run", "image"}, want: RiskHigh},
		{name: "docker run privileged", args: []string{"docker", "run", "--privileged", "image"}, want: RiskCritical},
		{name: "systemctl status", args: []string{"systemctl", "status", "nginx"}, want: RiskLow},
		{
			name: "systemctl flag then status",
			args: []string{"systemctl", "--no-pager", "status", "nginx"},
			want: RiskLow,
		},
		{name: "systemctl start", args: []string{"systemctl", "start", "nginx"}, want: RiskHigh},
		{name: "systemctl poweroff", args: []string{"systemctl", "poweroff"}, want: RiskCritical},
		{name: "kubectl get", args: []string{"kubectl", "get", "pods"}, want: RiskLow},
		{
			name: "kubectl namespace then describe",
			args: []string{"kubectl", "-n", "prod", "describe", "pod", "x"},
			want: RiskLow,
		},
		{name: "kubectl delete", args: []string{"kubectl", "delete", "pod", "x"}, want: RiskHigh},
		{name: "git status", args: []string{"git", "status"}, want: RiskLow},
		{name: "git log", args: []string{"git", "log", "--oneline"}, want: RiskLow},
		{name: "git diff", args: []string{"git", "diff"}, want: RiskLow},
		{name: "git -C then status", args: []string{"git", "-C", "/tmp", "status"}, want: RiskLow},
		{name: "git branch delete", args: []string{"git", "branch", "-D", "feature"}, want: RiskHigh},
		{name: "git branch delete lower", args: []string{"git", "branch", "-d", "feature"}, want: RiskHigh},
		{
			name: "git push force with lease",
			args: []string{"git", "push", "--force-with-lease", "origin"},
			want: RiskHigh,
		},
		{name: "git push force", args: []string{"git", "push", "--force", "origin"}, want: RiskCritical},
		{name: "rm base", args: []string{"rm", "file"}, want: RiskMedium},
		{name: "rm force only", args: []string{"rm", "-f", "file"}, want: RiskMedium},
		{name: "rm recursive", args: []string{"rm", "-r", "build"}, want: RiskHigh},
		{name: "rm recursive force", args: []string{"rm", "-rf", "build"}, want: RiskCritical},
		{name: "rm long cluster", args: []string{"rm", "-rfvv", "build"}, want: RiskCritical},
		{name: "wipefs", args: []string{"wipefs", "/dev/sda"}, want: RiskCritical},
		{name: "blkdiscard", args: []string{"blkdiscard", "/dev/sda"}, want: RiskCritical},
		{name: "lvremove", args: []string{"lvremove", "vg/lv"}, want: RiskCritical},
		{name: "cryptsetup status", args: []string{"cryptsetup", "status", "/dev/sda"}, want: RiskLow},
		{name: "cryptsetup luksFormat", args: []string{"cryptsetup", "luksFormat", "/dev/sda"}, want: RiskCritical},
		{name: "cryptsetup luksOpen", args: []string{"cryptsetup", "luksOpen", "/dev/sda", "name"}, want: RiskHigh},
		{name: "zpool status", args: []string{"zpool", "status"}, want: RiskLow},
		{name: "zpool destroy", args: []string{"zpool", "destroy", "tank"}, want: RiskCritical},
		{name: "zpool create", args: []string{"zpool", "create", "tank", "disk"}, want: RiskHigh},
		{name: "mdadm detail", args: []string{"mdadm", "--detail", "/dev/md0"}, want: RiskLow},
		{name: "mdadm device then detail", args: []string{"mdadm", "/dev/md0", "--detail"}, want: RiskLow},
		{name: "mdadm zero superblock", args: []string{"mdadm", "--zero-superblock", "/dev/sda"}, want: RiskCritical},
		{name: "mdadm create", args: []string{"mdadm", "--create", "/dev/md0"}, want: RiskHigh},
		{name: "terraform plan", args: []string{"terraform", "plan"}, want: RiskLow},
		{name: "terraform apply", args: []string{"terraform", "apply"}, want: RiskHigh},
		{name: "terraform destroy", args: []string{"terraform", "destroy"}, want: RiskCritical},
		{name: "terraform state rm", args: []string{"terraform", "state", "rm", "x"}, want: RiskHigh},
		{name: "helm list", args: []string{"helm", "list"}, want: RiskLow},
		{name: "helm uninstall", args: []string{"helm", "uninstall", "release"}, want: RiskHigh},
		{name: "helm install", args: []string{"helm", "install", "release", "chart"}, want: RiskHigh},
		{name: "aws s3 ls", args: []string{"aws", "s3", "ls"}, want: RiskMedium},
		{name: "aws s3 rm", args: []string{"aws", "s3", "rm", "s3://bucket/key"}, want: RiskHigh},
		{name: "aws s3 rb", args: []string{"aws", "s3", "rb", "s3://bucket"}, want: RiskHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyCommand(tt.args, nil); got != tt.want {
				t.Fatalf("ClassifyCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestClassifySubcommandOverridePrecedence(t *testing.T) {
	t.Parallel()

	lowDocker := map[string]string{"docker": "low"}
	if got := ClassifyCommand([]string{"docker", "ps"}, lowDocker); got != RiskLow {
		t.Fatalf("docker ps with docker=low = %v, want low", got)
	}
	if got := ClassifyCommand([]string{"docker", "run", "image"}, lowDocker); got != RiskHigh {
		t.Fatalf("docker run with docker=low = %v, want high (subcommand floor)", got)
	}

	highDocker := map[string]string{"docker": "high"}
	if got := ClassifyCommand([]string{"docker", "ps"}, highDocker); got != RiskHigh {
		t.Fatalf("docker ps with docker=high = %v, want high (override floor)", got)
	}
}

func TestClassifyCommandOverridesAndModifiers(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{"rm": "medium", "my-tool": "critical"}

	if got := ClassifyCommand([]string{"rm", "file"}, overrides); got != RiskMedium {
		t.Fatalf("override rm = %v, want medium", got)
	}
	if got := ClassifyCommand([]string{"rm", "-rf", "build"}, overrides); got != RiskCritical {
		t.Fatalf("modifier must still elevate an overridden base, got %v", got)
	}
	if got := ClassifyCommand([]string{"my-tool"}, overrides); got != RiskCritical {
		t.Fatalf("override my-tool = %v, want critical", got)
	}

	extra := map[string][]ArgModifier{
		"echo": {{Args: []string{"--danger"}, Level: RiskHigh}},
	}
	if got := ClassifyCommand([]string{"echo", "--danger"}, nil, extra); got != RiskHigh {
		t.Fatalf("extra modifier = %v, want high", got)
	}
	if got := ClassifyCommand([]string{"echo", "safe"}, nil, extra); got != RiskLow {
		t.Fatalf("extra modifier should not apply, got %v", got)
	}
}

func TestNormalizeCommandKeys(t *testing.T) {
	t.Parallel()

	got := NormalizeCommandKeys(map[string]string{
		"/bin/rm": "high",
		"git":     "medium",
	})
	if got["rm"] != "high" || got["git"] != "medium" {
		t.Fatalf("NormalizeCommandKeys = %#v", got)
	}
}

func TestBaseCommand(t *testing.T) {
	t.Parallel()

	if got := baseCommand("/usr/bin/rm"); got != "rm" {
		t.Fatalf("baseCommand = %q, want rm", got)
	}
	if got := baseCommand(""); got != "" {
		t.Fatalf("baseCommand(\"\") = %q, want empty", got)
	}
	if runtime.GOOS != "windows" {
		if got := baseCommand("ls.exe"); got != "ls.exe" {
			t.Fatalf("Unix baseCommand must not strip extensions, got %q", got)
		}
	}
}

func TestNormalizeFlags(t *testing.T) {
	t.Parallel()

	got := normalizeFlags([]string{"-rf", "--request=POST", "file"})
	want := []string{"-r", "-f", "--request=POST", "file"}
	if len(got) != len(want) {
		t.Fatalf("normalizeFlags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeFlags = %v, want %v", got, want)
		}
	}
}

func TestMatchArgsAttachedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		pattern []string
		want    bool
	}{
		{
			name:    "long attached value",
			args:    []string{"--request=POST", "https://example.com"},
			pattern: []string{"--request", "POST"},
			want:    true,
		},
		{
			name:    "short attached value",
			args:    []string{"-XPOST", "https://example.com"},
			pattern: []string{"-X", "POST"},
			want:    true,
		},
		{
			name:    "short attached value without pair",
			args:    []string{"-d@file"},
			pattern: []string{"-d"},
			want:    true,
		},
		{
			name:    "spaced value",
			args:    []string{"-X", "POST"},
			pattern: []string{"-X", "POST"},
			want:    true,
		},
		{
			name:    "flag equals does not become positional",
			args:    []string{"log", "--grep=push"},
			pattern: []string{"push"},
			want:    false,
		},
		{
			name:    "boolean flag with value matches flag",
			args:    []string{"--force=true"},
			pattern: []string{"--force"},
			want:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := matchArgs(tt.args, tt.pattern); got != tt.want {
				t.Fatalf("matchArgs(%v, %v) = %v, want %v", tt.args, tt.pattern, got, tt.want)
			}
		})
	}
}
