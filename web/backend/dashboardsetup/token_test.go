package dashboardsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	first, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	second, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	if len(first) != 43 {
		t.Fatalf("token length = %d, want 43", len(first))
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("token is not raw URL-safe base64: %q", first)
	}
	if first == second {
		t.Fatal("successive tokens are identical")
	}
}

func TestWriteToken(t *testing.T) {
	path := TokenPath(filepath.Join(t.TempDir(), "home"))

	if err := WriteToken(path, "first-token"); err != nil {
		t.Fatalf("WriteToken() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "first-token\n" {
		t.Fatalf("token file = %q", data)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	assertTokenPermissions(t, path)

	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	if err = WriteToken(path, "second-token"); err != nil {
		t.Fatalf("overwrite WriteToken() error = %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() after overwrite error = %v", err)
	}
	if string(data) != "second-token\n" {
		t.Fatalf("overwritten token file = %q", data)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatalf("Stat() after overwrite error = %v", err)
	}
	assertTokenPermissions(t, path)
}

func TestWriteTokenRejectsEmptyToken(t *testing.T) {
	path := TokenPath(t.TempDir())
	if err := WriteToken(path, "  "); err == nil {
		t.Fatal("WriteToken() accepted an empty token")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should not exist, Stat error = %v", err)
	}
}
