package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestUnisolatedChildUsesPrivateScratch(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("TMPDIR", "/host-temp")
	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = false
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })
	cmd := exec.Command("unused")
	cmd.Env = []string{"TMPDIR=/other", "KEEP=value"}
	if err := PrepareCommand(cmd); err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string)
	for _, item := range cmd.Env {
		k, v, _ := strings.Cut(item, "=")
		values[k] = v
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(canonicalHome, values["TMPDIR"])
	if err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("scratch escaped home: %v", values)
	}
	if values["TMP"] != values["TMPDIR"] || values["TEMP"] != values["TMPDIR"] {
		t.Fatalf("inconsistent scratch: %v", values)
	}
	if values["KEEP"] != "value" || os.Getenv("TMPDIR") != "/host-temp" {
		t.Fatal("unrelated or host environment changed")
	}
}
