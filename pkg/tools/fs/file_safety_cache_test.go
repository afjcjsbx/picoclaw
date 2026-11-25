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

func TestCredentialRootsCacheTracksUserHome(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", "")
	t.Setenv("CODEX_HOME", "")
	userA := t.TempDir()
	userB := t.TempDir()
	t.Setenv("HOME", userA)
	t.Setenv("USERPROFILE", userA)

	if !isSensitiveReadPath(filepath.Join(userA, ".picoclaw", "auth.json")) {
		t.Fatal("default app home under user A was not protected")
	}
	t.Setenv("HOME", userB)
	t.Setenv("USERPROFILE", userB)
	if !isSensitiveReadPath(filepath.Join(userB, ".picoclaw", "auth.json")) {
		t.Fatal("default app home under user B was not protected")
	}
	if isSensitiveReadPath(filepath.Join(userA, ".picoclaw", "auth.json")) {
		t.Fatal("default app home under user A stayed protected after the switch")
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

func TestAppCredentialRulesResolveSymlinkedAndMissingRootsLive(t *testing.T) {
	home := canonicalTempDir(t)
	external := canonicalTempDir(t)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(home, "auth", "mcp")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rules, live := buildAppCredentialRules(home, "")
	if len(rules) == 0 {
		t.Fatal("lexical app rules were not built")
	}
	if !containsLivePath(live, filepath.Join(home, "auth", "mcp")) {
		t.Fatal("symlinked protected root was cached instead of resolved live")
	}
	if !containsLivePath(live, filepath.Join(home, "plugin-data")) {
		t.Fatal("missing protected root was cached instead of resolved live")
	}
	if containsLivePath(live, filepath.Join(home, "auth.json")) {
		t.Fatal("regular protected root was marked for live resolution")
	}
}

func TestSensitiveReadResolvesRetargetedCredentialSymlinkLive(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(other, "codex"))
	oldTarget := filepath.Join(other, "creds-old")
	newTarget := filepath.Join(other, "creds-new")
	for _, path := range []string{oldTarget, newTarget} {
		if err := os.WriteFile(path, []byte("SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "auth.json")
	if err := os.Symlink(oldTarget, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := checkSensitiveReadPath(oldTarget, "", false); err == nil {
		t.Fatal("initial symlink target was not protected")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(newTarget, link); err != nil {
		t.Fatal(err)
	}
	if err := checkSensitiveReadPath(newTarget, "", false); err == nil {
		t.Fatal("retargeted credential escaped the policy inside the cache window")
	}
}

func TestSensitiveReadResolvesCreatedCredentialSymlinkLive(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(other, "codex"))
	target := filepath.Join(other, "creds")
	if err := os.WriteFile(target, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkSensitiveReadPath(filepath.Join(home, "unrelated"), "", false); err != nil {
		t.Fatalf("unexpected sensitive path: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(home, "auth.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := checkSensitiveReadPath(target, "", false); err == nil {
		t.Fatal("credential symlink created after the snapshot escaped the policy")
	}
}

func containsLivePath(paths []protectedLiveRoot, want string) bool {
	for _, path := range paths {
		if path.path == want {
			return true
		}
	}
	return false
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
