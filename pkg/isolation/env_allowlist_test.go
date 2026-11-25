package isolation

import (
	"os"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestIsParentEnvAllowed(t *testing.T) {
	allowed := []string{
		"PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "SHELL", "TMPDIR",
		"XDG_CACHE_HOME", "XDG_STATE_HOME",
	}
	for _, key := range allowed {
		if !IsParentEnvAllowed(key) {
			t.Errorf("IsParentEnvAllowed(%q) = false, want true", key)
		}
	}
	blocked := []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "HOST_SECRET", "AWS_SECRET_ACCESS_KEY"}
	for _, key := range blocked {
		if IsParentEnvAllowed(key) {
			t.Errorf("IsParentEnvAllowed(%q) = true, want false", key)
		}
	}
}

func TestIsExtraAllowed(t *testing.T) {
	extra := []string{"ANTHROPIC_*", "HTTP_PROXY", "HTTPS_PROXY"}

	cases := []struct {
		key  string
		want bool
	}{
		{"ANTHROPIC_API_KEY", true},
		{"ANTHROPIC_BASE_URL", true},
		{"HTTP_PROXY", true},
		{"HTTPS_PROXY", true},
		{"NO_PROXY", false},
		{"OPENAI_API_KEY", false},
		{"ANTHROPIC", false}, // no trailing part after prefix
	}
	for _, tc := range cases {
		got := isExtraAllowed(tc.key, extra)
		if got != tc.want {
			t.Errorf("isExtraAllowed(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestInheritedEnv_Disabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = false
	Configure(cfg)
	t.Cleanup(func() { Configure(nil) })

	t.Setenv("OPENAI_API_KEY", "sk-should-appear-disabled")

	got := InheritedEnv()
	// When disabled, should equal os.Environ() exactly.
	full := os.Environ()
	if len(got) != len(full) {
		t.Fatalf("InheritedEnv (disabled) len=%d, want %d (os.Environ)", len(got), len(full))
	}
	for _, want := range full {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("InheritedEnv (disabled) missing entry %q", want)
		}
	}
}

func TestInheritedEnv_Enabled_FiltersSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PICOCLAW_HOME", root)

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	Configure(cfg)
	t.Cleanup(func() { Configure(nil) })

	t.Setenv("OPENAI_API_KEY", "sk-leak-test")
	t.Setenv("HOST_SECRET_MARKER", "xyz")
	t.Setenv("PATH", "/usr/bin:/bin")

	got := InheritedEnv()

	for _, entry := range got {
		idx := strings.IndexByte(entry, '=')
		if idx <= 0 {
			continue
		}
		key := entry[:idx]
		if key == "OPENAI_API_KEY" {
			t.Errorf("InheritedEnv (enabled) leaked OPENAI_API_KEY")
		}
		if key == "HOST_SECRET_MARKER" {
			t.Errorf("InheritedEnv (enabled) leaked HOST_SECRET_MARKER")
		}
	}

	pathFound := false
	for _, entry := range got {
		if strings.HasPrefix(entry, "PATH=") {
			pathFound = true
			break
		}
	}
	if !pathFound {
		t.Error("InheritedEnv (enabled) missing PATH")
	}
}

func TestInheritedEnv_Enabled_Extra(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PICOCLAW_HOME", root)

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	Configure(cfg)
	t.Cleanup(func() { Configure(nil) })

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-should-not-appear")
	t.Setenv("HTTP_PROXY", "http://proxy:8080")
	t.Setenv("PICOCLAW_SECRET", "secret")

	got := InheritedEnv("ANTHROPIC_*", "HTTP_PROXY")

	keys := make(map[string]string, len(got))
	for _, entry := range got {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			keys[entry[:idx]] = entry[idx+1:]
		}
	}

	if _, ok := keys["ANTHROPIC_API_KEY"]; !ok {
		t.Error("InheritedEnv: ANTHROPIC_API_KEY missing despite ANTHROPIC_* extra")
	}
	if _, ok := keys["HTTP_PROXY"]; !ok {
		t.Error("InheritedEnv: HTTP_PROXY missing despite explicit extra")
	}
	if _, ok := keys["OPENAI_API_KEY"]; ok {
		t.Error("InheritedEnv: OPENAI_API_KEY should not appear")
	}
	if _, ok := keys["PICOCLAW_SECRET"]; ok {
		t.Error("InheritedEnv: PICOCLAW_SECRET should not appear")
	}
}
