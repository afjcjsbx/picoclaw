package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCredentialRootsCacheInvalidation(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	codexA := t.TempDir()
	codexB := t.TempDir()
	t.Setenv("PICOCLAW_HOME", homeA)
	t.Setenv("CODEX_HOME", codexA)

	first := credentialRoots()
	if !isSensitiveReadPath(filepath.Join(homeA, "auth.json")) {
		t.Fatal("app home A credential was not protected")
	}
	if !isSensitiveReadPath(filepath.Join(codexA, "auth.json")) {
		t.Fatal("codex home A credential was not protected")
	}

	t.Setenv("PICOCLAW_HOME", homeB)
	second := credentialRoots()
	if second == first {
		t.Fatal("app home change did not invalidate the cache")
	}
	if !isSensitiveReadPath(filepath.Join(homeB, "auth.json")) {
		t.Fatal("app home B credential was not protected")
	}
	if isSensitiveReadPath(filepath.Join(homeA, "auth.json")) {
		t.Fatal("app home A credential stayed protected after the switch")
	}

	t.Setenv("CODEX_HOME", codexB)
	third := credentialRoots()
	if third == second {
		t.Fatal("CODEX_HOME change did not invalidate the cache")
	}
	if !isSensitiveReadPath(filepath.Join(codexB, "auth.json")) {
		t.Fatal("codex home B credential was not protected")
	}
	if isSensitiveReadPath(filepath.Join(codexA, "auth.json")) {
		t.Fatal("codex home A credential stayed protected after the switch")
	}
}

func TestCredentialRootsCacheExpiry(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())

	first := credentialRoots()
	first.built = time.Now().Add(-credentialRootsCacheTTL - time.Second)

	second := credentialRoots()
	if second == first {
		t.Fatal("expired cache was not rebuilt")
	}
	if third := credentialRoots(); third != second {
		t.Fatal("fresh cache was rebuilt before it expired")
	}
}

func TestProtectedReadDirHidesCredentialBehindSymlinkedDir(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result := NewListDirTool(workspace, false).Execute(
		context.Background(), map[string]any{"path": link},
	)
	if result.IsError {
		t.Fatalf("list_dir failed: %+v", result)
	}
	if strings.Contains(result.ForLLM, "auth.json") {
		t.Fatalf("listing a symlinked app home exposed auth.json: %s", result.ForLLM)
	}
}

func TestProtectedReadDirHidesSymlinkToCredential(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "normal.txt"), []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "innocuous")
	if err := os.Symlink(filepath.Join(home, "auth.json"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result := NewListDirTool(workspace, false).Execute(
		context.Background(), map[string]any{"path": workspace},
	)
	if result.IsError {
		t.Fatalf("list_dir failed: %+v", result)
	}
	if strings.Contains(result.ForLLM, "innocuous") {
		t.Fatalf("directory listing exposed a symlink to a credential: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "normal.txt") {
		t.Fatalf("directory listing dropped an ordinary file: %s", result.ForLLM)
	}
}
