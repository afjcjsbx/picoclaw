package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingFileBoundsSizeAndBackupCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.log")
	w, err := newRotatingFile(path, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first123", "second2", "third33"} {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if closeErr := w.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	for suffix, want := range map[string]string{"": "third33", ".1": "second2", ".2": "first123"} {
		got, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatalf("read %s: %v", path+suffix, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path+suffix, got, want)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: err = %v", err)
	}
}

func TestRotatingFileTrimsAnOversizedExistingLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := os.WriteFile(path, []byte("first line\nsecond line\nthird line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := newRotatingFile(path, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := w.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 16 {
		t.Fatalf("trimmed log size = %d, want <= 16", info.Size())
	}
}
