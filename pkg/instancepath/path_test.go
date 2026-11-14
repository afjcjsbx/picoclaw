package instancepath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStorageFollowsInstanceHome(t *testing.T) {
	globalTemp := t.TempDir()
	t.Setenv("TMPDIR", globalTemp)
	var previous string
	for range 2 {
		home := t.TempDir()
		t.Setenv(EnvHome, home)
		f, err := CreateTemp("download-*")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		rel, err := filepath.Rel(home, f.Name())
		if err != nil || !filepath.IsLocal(rel) {
			t.Fatalf("file escaped instance: %s", f.Name())
		}
		if filepath.Dir(f.Name()) == previous {
			t.Fatal("different instances share scratch")
		}
		previous = filepath.Dir(f.Name())
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v", info.Mode())
		}
	}
	entries, err := os.ReadDir(globalTemp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("system temp was used: %v, %v", entries, err)
	}
}

func TestPrivateDirRejectsSymlinks(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	t.Setenv(EnvHome, home)
	if err := os.Symlink(outside, filepath.Join(home, "tmp")); err != nil {
		t.Skip(err)
	}
	if _, err := TempDir("media"); err == nil {
		t.Fatal("accepted symlinked tmp directory")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside home: %v %v", entries, err)
	}
}

func TestPrivateDirRepairsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	dir := filepath.Join(home, "tmp")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := TempDir("media"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private permissions: %v %v", info, err)
	}
}

func TestMissingHomeDoesNotUseCWD(t *testing.T) {
	for _, key := range []string{EnvHome, "HOME", "USERPROFILE", "home"} {
		t.Setenv(key, "")
	}
	if _, err := CreateTemp("test-*"); err == nil {
		t.Fatal("missing home accepted")
	}
	t.Setenv(EnvHome, ".")
	if _, err := TempDir("media"); err == nil {
		t.Fatal("cwd accepted as instance home")
	}
}
