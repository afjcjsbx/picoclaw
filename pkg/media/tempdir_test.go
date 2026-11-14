package media

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

func TestWriteTempRemovesPartialFile(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", t.TempDir())
	wantErr := errors.New("download interrupted")
	_, err := WriteTemp("partial-*", io.MultiReader(strings.NewReader("partial data"), iotest.ErrReader(wantErr)))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v", err)
	}
	dir, err := TempDir()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "partial-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("partial files remain: %v, %v", files, err)
	}
}

func TestMediaIgnoresSystemTempAndDoesNotOverwrite(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", t.TempDir())
	legacy := t.TempDir()
	t.Setenv("TMPDIR", legacy)
	// Even an unusable old shared location must not affect media staging.
	if err := os.WriteFile(filepath.Join(legacy, "picoclaw_media"), []byte("another user"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := WriteTemp("photo-*.png", strings.NewReader("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := WriteTemp("photo-*.png", strings.NewReader("second"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("media filenames collide")
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "first" {
		t.Fatalf("first file overwritten: %q, %v", data, err)
	}
}
