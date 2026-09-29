package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ path, want string }{
		{"", ""},
		{"relative", "relative"},
		{"~other", "~other"},
		{"~", home},
		{"~/folder", home + "/folder"},
	} {
		if got := ExpandHome(tt.path); got != tt.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
	t.Setenv("HOME", "")
	if got := ExpandHome("~/folder"); got != "~/folder" {
		t.Errorf("ExpandHome without a home directory = %q, want unchanged path", got)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !Exists(file) || Exists(dir) || Exists(filepath.Join(dir, "missing")) {
		t.Fatal("Exists should accept files and reject directories or missing paths")
	}
}
