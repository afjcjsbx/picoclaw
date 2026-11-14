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
		{name: "known high", args: []string{"rm", "file"}, want: RiskHigh},
		{name: "known critical", args: []string{"sudo", "ls"}, want: RiskCritical},
		{name: "unknown defaults to medium", args: []string{"some-new-tool"}, want: RiskMedium},
		{name: "empty defaults to medium", args: nil, want: RiskMedium},
		{name: "path basename", args: []string{"/bin/rm", "file"}, want: RiskHigh},
		{name: "rm recursive force", args: []string{"rm", "-rf", "build"}, want: RiskCritical},
		{name: "rm force recursive", args: []string{"rm", "-fr", "build"}, want: RiskCritical},
		{name: "rm uppercase recursive force", args: []string{"rm", "-Rf", "build"}, want: RiskCritical},
		{name: "rm split flags", args: []string{"rm", "-r", "-f", "build"}, want: RiskCritical},
		{name: "rm long flags", args: []string{"rm", "--recursive", "--force", "build"}, want: RiskCritical},
		{name: "git push", args: []string{"git", "push", "origin", "main"}, want: RiskHigh},
		{name: "git push force", args: []string{"git", "push", "--force", "origin"}, want: RiskCritical},
		{name: "git status", args: []string{"git", "status"}, want: RiskMedium},
		{name: "curl get", args: []string{"curl", "https://example.com"}, want: RiskMedium},
		{name: "curl post", args: []string{"curl", "-X", "POST", "https://example.com"}, want: RiskHigh},
		{name: "curl attached post", args: []string{"curl", "-XPOST", "https://example.com"}, want: RiskHigh},
		{name: "curl attached data", args: []string{"curl", "-d@file", "https://example.com"}, want: RiskHigh},
		{name: "curl long equals", args: []string{"curl", "--request=POST", "https://example.com"}, want: RiskHigh},
		{name: "git log grep is not push", args: []string{"git", "log", "--grep=push"}, want: RiskMedium},
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
