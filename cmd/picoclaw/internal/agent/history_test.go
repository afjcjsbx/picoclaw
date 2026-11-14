package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryUsesInstanceHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	path, err := prepareHistoryFile()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, "history") {
		t.Fatalf("history = %q", path)
	}
	if err := os.WriteFile(path, []byte("keep history"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareHistoryFile(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep history" {
		t.Fatalf("existing history lost: %q, %v", data, err)
	}
}

func TestHistoryRejectsSymlink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "history")); err != nil {
		t.Skip(err)
	}
	if _, err := prepareHistoryFile(); err == nil {
		t.Fatal("symlinked history accepted")
	}
}
