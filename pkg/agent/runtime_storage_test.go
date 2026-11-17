package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/instancepath"
	"github.com/sipeed/picoclaw/pkg/media"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

func TestRuntimeReadAllowancesStayNarrow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	patterns := buildAllowReadPatterns(nil)
	dir, err := media.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := instancepath.PrivateDir("artifacts", "tool")
	if err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []string{filepath.Join(dir, "photo.png"), filepath.Join(artifact, "output.log")} {
		if !fstools.IsAllowedPath(allowed, patterns) {
			t.Fatalf("runtime output denied: %s", allowed)
		}
	}
	for _, denied := range []string{
		filepath.Join(home, "auth.json"), filepath.Join(home, "tmp", "work", "download"),
		filepath.Join(home, "tmp", "media", "run-other", "photo.png"),
	} {
		if fstools.IsAllowedPath(denied, patterns) {
			t.Fatalf("unrelated storage exposed: %s", denied)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "auth.json"), filepath.Join(dir, "escape")); err == nil {
		if fstools.IsAllowedPath(filepath.Join(dir, "escape"), patterns) {
			t.Fatal("symlink escape exposed")
		}
	}
}
