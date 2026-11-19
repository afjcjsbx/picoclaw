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
		name  string
		args  []string
		want  RiskLevel
		known bool
	}{
		{name: "low ls", args: []string{"ls", "-la"}, want: RiskLow, known: true},
		{name: "low cat", args: []string{"cat", "file"}, want: RiskLow, known: true},
		{name: "low grep", args: []string{"grep", "needle", "file"}, want: RiskLow, known: true},
		{name: "low rg", args: []string{"rg", "needle"}, want: RiskLow, known: true},
		{name: "low find", args: []string{"find", ".", "-name", "*.go"}, want: RiskLow, known: true},
		{name: "medium cp", args: []string{"cp", "a", "b"}, want: RiskMedium, known: true},
		{name: "medium git", args: []string{"git", "status"}, want: RiskMedium, known: true},
		{name: "medium systemctl", args: []string{"systemctl", "status", "nginx"}, want: RiskMedium, known: true},
		{name: "medium aws", args: []string{"aws", "s3", "ls"}, want: RiskMedium, known: true},
		{name: "medium cryptsetup", args: []string{"cryptsetup", "status", "/dev/sda"}, want: RiskMedium, known: true},
		{name: "medium zpool", args: []string{"zpool", "status"}, want: RiskMedium, known: true},
		{name: "medium mdadm", args: []string{"mdadm", "--detail", "/dev/md0"}, want: RiskMedium, known: true},
		{name: "medium lxc", args: []string{"lxc", "list"}, want: RiskMedium, known: true},
		{name: "medium virsh", args: []string{"virsh", "list"}, want: RiskMedium, known: true},
		{name: "medium rm", args: []string{"rm", "file"}, want: RiskMedium, known: true},
		{name: "high chmod", args: []string{"chmod", "+x", "script.sh"}, want: RiskHigh, known: true},
		{name: "high kill", args: []string{"kill", "1234"}, want: RiskHigh, known: true},
		{name: "high setpriv", args: []string{"setpriv", "--reuid", "0", "ls"}, want: RiskHigh, known: true},
		{name: "high capsh", args: []string{"capsh", "--print"}, want: RiskHigh, known: true},
		{name: "high setsid", args: []string{"setsid", "sleep", "300"}, want: RiskHigh, known: true},
		{name: "high unshare", args: []string{"unshare", "-p", "sleep", "300"}, want: RiskHigh, known: true},
		{name: "critical sudo", args: []string{"sudo", "ls"}, want: RiskCritical, known: true},
		{name: "critical dd", args: []string{"dd", "if=/dev/zero", "of=/dev/sda"}, want: RiskCritical, known: true},
		{name: "critical mkfs", args: []string{"mkfs.ext4", "/dev/sda1"}, want: RiskCritical, known: true},
		{name: "critical shutdown", args: []string{"shutdown", "-h", "now"}, want: RiskCritical, known: true},
		{name: "critical shell", args: []string{"bash", "script.sh"}, want: RiskCritical, known: true},
		{name: "critical iptables", args: []string{"iptables", "-F"}, want: RiskCritical, known: true},
		{name: "unknown tool", args: []string{"some-new-tool", "--flag"}, want: RiskMedium, known: false},
		{name: "unknown path", args: []string{"/opt/new-tool"}, want: RiskMedium, known: false},
		{name: "docker is not classified", args: []string{"docker", "ps"}, want: RiskMedium, known: false},
		{name: "kubectl is not classified", args: []string{"kubectl", "get", "pods"}, want: RiskMedium, known: false},
		{name: "terraform is not classified", args: []string{"terraform", "plan"}, want: RiskMedium, known: false},
		{name: "helm is not classified", args: []string{"helm", "list"}, want: RiskMedium, known: false},
		{name: "empty", args: nil, want: RiskMedium, known: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, known := classifyCommand(tt.args)
			if got != tt.want || known != tt.known {
				t.Fatalf(
					"classifyCommand(%v) = (%v, %v), want (%v, %v)",
					tt.args, got, known, tt.want, tt.known,
				)
			}
		})
	}
}

func TestClassifyModifiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want RiskLevel
	}{
		{name: "rm recursive force", args: []string{"rm", "-rf", "build"}, want: RiskCritical},
		{name: "rm force recursive", args: []string{"rm", "-fr", "build"}, want: RiskCritical},
		{name: "rm uppercase recursive force", args: []string{"rm", "-Rf", "build"}, want: RiskCritical},
		{name: "rm split flags", args: []string{"rm", "-r", "-f", "build"}, want: RiskCritical},
		{name: "rm long flags", args: []string{"rm", "--recursive", "--force", "build"}, want: RiskCritical},
		{name: "git push", args: []string{"git", "push", "origin", "main"}, want: RiskHigh},
		{name: "git push force", args: []string{"git", "push", "--force", "origin"}, want: RiskCritical},
		{name: "git branch delete", args: []string{"git", "branch", "-D", "feature"}, want: RiskHigh},
		{name: "git clean", args: []string{"git", "clean", "-fdx"}, want: RiskHigh},
		{name: "curl post", args: []string{"curl", "-X", "POST", "https://example.com"}, want: RiskHigh},
		{name: "sed in place", args: []string{"sed", "-i", "s/a/b/", "file"}, want: RiskMedium},
		{name: "npm global install", args: []string{"npm", "install", "-g", "pkg"}, want: RiskHigh},
		{name: "apt purge", args: []string{"apt", "purge", "pkg"}, want: RiskCritical},
		{name: "find delete", args: []string{"find", ".", "-delete"}, want: RiskHigh},
		{name: "find exec", args: []string{"find", ".", "-exec", "rm", "{}", ";"}, want: RiskHigh},
		{name: "kill sigkill", args: []string{"kill", "-9", "1234"}, want: RiskCritical},
		{name: "systemctl start", args: []string{"systemctl", "start", "nginx"}, want: RiskHigh},
		{name: "systemctl poweroff", args: []string{"systemctl", "poweroff"}, want: RiskCritical},
		{name: "aws s3 rm", args: []string{"aws", "s3", "rm", "s3://bucket/key"}, want: RiskHigh},
		{name: "aws s3api delete", args: []string{"aws", "s3api", "delete-object", "--bucket", "b"}, want: RiskHigh},
		{
			name: "aws ec2 terminate",
			args: []string{"aws", "ec2", "terminate-instances", "--instance-ids", "i-1"},
			want: RiskHigh,
		},
		{name: "cryptsetup luksFormat", args: []string{"cryptsetup", "luksFormat", "/dev/sda"}, want: RiskCritical},
		{name: "zpool destroy", args: []string{"zpool", "destroy", "tank"}, want: RiskCritical},
		{name: "mdadm zero superblock", args: []string{"mdadm", "--zero-superblock", "/dev/sda"}, want: RiskCritical},
		{name: "lxc delete", args: []string{"lxc", "delete", "c1"}, want: RiskHigh},
		{name: "incus delete", args: []string{"incus", "delete", "c1"}, want: RiskHigh},
		{name: "machinectl shell", args: []string{"machinectl", "shell", "user@m", "ls"}, want: RiskHigh},
		{name: "virsh destroy", args: []string{"virsh", "destroy", "vm"}, want: RiskHigh},
		{
			name: "virsh monitor command",
			args: []string{"virsh", "qemu-monitor-command", "vm", "info"},
			want: RiskCritical,
		},
		{name: "busybox install", args: []string{"busybox", "--install", "-s"}, want: RiskHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, known := classifyCommand(tt.args)
			if !known {
				t.Fatalf("classifyCommand(%v) = unknown, want a known command", tt.args)
			}
			if got != tt.want {
				t.Fatalf("classifyCommand(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
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

// TestDestructiveOperandCommandsAreClassified keeps the two command lists in
// step: a command whose dynamic operand is rejected must also have a risk
// level of its own.
func TestDestructiveOperandCommandsAreClassified(t *testing.T) {
	t.Parallel()

	for name := range destructiveOperandCommands {
		if _, known := classifyCommand([]string{name}); !known {
			t.Errorf("%s is in destructiveOperandCommands but not in the risk table", name)
		}
	}
}
