package fstools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/afjcjsbx/picoclaw/pkg/media"
)

func TestFileSafetyProtectedWorkspaceReads(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".envrc", ".security.yml", ".netrc"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("TOP_SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(workspace, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".aws", "credentials"), []byte("TOP_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "normal.txt"), []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	protectedPaths := []string{".env", ".env.local", ".envrc", ".security.yml", ".netrc", ".aws/credentials"}
	if err := os.Symlink(".env", filepath.Join(workspace, "alias")); err == nil {
		protectedPaths = append(protectedPaths, "alias")
	}

	for _, restrict := range []bool{false, true} {
		for _, name := range protectedPaths {
			path := filepath.Join(workspace, name)
			tool := NewReadFileTool(workspace, restrict, MaxReadFileSize, []*regexp.Regexp{regexp.MustCompile(".*")})
			result := tool.Execute(context.Background(), map[string]any{"path": path})
			if !result.IsError || !strings.Contains(result.ForLLM, "access denied") ||
				strings.Contains(result.ForLLM, "TOP_SECRET") {
				t.Fatalf("read_file(%q, restrict=%v) = %+v", name, restrict, result)
			}
		}
	}

	read := NewReadFileTool(workspace, true, MaxReadFileSize)
	result := read.Execute(context.Background(), map[string]any{"path": "normal.txt"})
	if result.IsError || !strings.Contains(result.ForLLM, "public") {
		t.Fatalf("ordinary read failed: %+v", result)
	}
	linesResult := NewReadFileLinesTool(
		workspace,
		true,
		MaxReadFileSize,
	).Execute(context.Background(), map[string]any{"path": ".env"})
	if !linesResult.IsError {
		t.Fatalf("line-based read exposed .env: %+v", linesResult)
	}
	list := NewListDirTool(workspace, true)
	result = list.Execute(context.Background(), map[string]any{"path": "."})
	if result.IsError || !strings.Contains(result.ForLLM, "normal.txt") || strings.Contains(result.ForLLM, ".env") ||
		strings.Contains(result.ForLLM, ".aws") ||
		strings.Contains(result.ForLLM, "alias") {
		t.Fatalf("directory listing exposed protected paths: %+v", result)
	}
	if result := list.Execute(context.Background(), map[string]any{"path": ".aws"}); !result.IsError {
		t.Fatalf("protected directory was listed: %+v", result)
	}

	search := NewSearchFilesTool(workspace, true)
	for _, args := range []map[string]any{
		{"pattern": "TOP_SECRET"},
		{"pattern": "*", "target": "files"},
	} {
		result := search.Execute(context.Background(), args)
		if result.IsError || strings.Contains(result.ForLLM, "TOP_SECRET") || strings.Contains(result.ForLLM, ".env") ||
			strings.Contains(result.ForLLM, ".aws") ||
			strings.Contains(result.ForLLM, "alias") {
			t.Fatalf("search exposed protected paths: %+v", result)
		}
	}
	for _, target := range []string{"content", "files"} {
		pattern := "TOP_SECRET"
		if target == "files" {
			pattern = "*"
		}
		result := search.Execute(
			context.Background(),
			map[string]any{"path": ".env", "pattern": pattern, "target": target},
		)
		if !result.IsError {
			t.Fatalf("direct search of protected path succeeded: %+v", result)
		}
	}
}

func TestFileSafetyBlocksProtectedWrites(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".env")
	write := NewWriteFileTool(workspace, true)
	if result := write.Execute(
		context.Background(),
		map[string]any{"path": path, "content": "TOKEN=old"},
	); !result.IsError || !strings.Contains(result.ForLLM, "write denied") {
		t.Fatalf("create .env was not blocked: %+v", result)
	}
	for _, tool := range []interface {
		Execute(ctx context.Context, args map[string]any) *ToolResult
	}{
		NewEditFileTool(workspace, true), NewAppendFileTool(workspace, true),
	} {
		result := tool.Execute(
			context.Background(),
			map[string]any{"path": path, "old_text": "old", "new_text": "new", "content": "more"},
		)
		if !result.IsError || !strings.Contains(result.ForLLM, "access denied") {
			t.Fatalf("read-modify-write exposed .env: %+v", result)
		}
	}
}

func TestFileSafetyBlocksProtectedMedia(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte("TOP_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	send := NewSendFileTool(workspace, true, 0, store)
	send.SetContext("test", "chat")
	load := NewLoadImageTool(workspace, true, 0, store)
	load.SetContext("test", "chat")
	for _, tool := range []interface {
		Execute(ctx context.Context, args map[string]any) *ToolResult
	}{send, load} {
		result := tool.Execute(context.Background(), map[string]any{"path": ".env"})
		if !result.IsError || !strings.Contains(result.ForLLM, "access denied") {
			t.Fatalf("media tool accepted .env: %+v", result)
		}
	}
}

func TestStripExtendedDrivePrefix(t *testing.T) {
	for in, want := range map[string]string{
		`\\?\C:\Users\u\.picoclaw\auth.json`: `C:\Users\u\.picoclaw\auth.json`,
		`//?/c:/Users/u/.picoclaw/auth.json`: `c:\Users\u\.picoclaw\auth.json`,
		`C:\Users\u\auth.json`:               `C:\Users\u\auth.json`,
		`\\?\UNC\host\share\x`:               `\\?\UNC\host\share\x`,
		`/home/u/auth.json`:                  `/home/u/auth.json`,
	} {
		if got := stripExtendedDrivePrefix(in); got != want {
			t.Errorf("stripExtendedDrivePrefix(%q) = %q, want %q", in, got, want)
		}
	}
	if isNTNamespacePath(`\\?\C:\x`) || !isNTNamespacePath(`\\?\UNC\h\s`) {
		t.Error("drive-letter extended paths must pass the NT check; UNC must not")
	}
}

func TestFileSafetySymlinkedCredentialTargets(t *testing.T) {
	for _, name := range []string{"auth.json", "auth/mcp", "plugin-data", "channels/weixin/context-tokens", "codex/auth.json"} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			home := t.TempDir()
			codexHome := t.TempDir()
			t.Setenv("PICOCLAW_HOME", home)
			t.Setenv("CODEX_HOME", codexHome)
			root := filepath.Join(workspace, "cache")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "token.txt")
			if err := os.WriteFile(path, []byte("TOP_SECRET"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(home, filepath.FromSlash(name))
			target := root
			if name == "auth.json" || name == "codex/auth.json" {
				target = path
			}
			if name == "codex/auth.json" {
				link = filepath.Join(codexHome, "auth.json")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			for _, restrict := range []bool{false, true} {
				patterns := []*regexp.Regexp{regexp.MustCompile(".*")}
				read := NewReadFileTool(workspace, restrict, MaxReadFileSize, patterns)
				if result := read.Execute(context.Background(), map[string]any{"path": path}); !result.IsError {
					t.Errorf("read allowed, restrict=%v: %+v", restrict, result)
				}
				for _, tool := range []interface {
					Execute(ctx context.Context, args map[string]any) *ToolResult
				}{
					NewWriteFileTool(workspace, restrict, patterns),
					NewEditFileTool(workspace, restrict, patterns),
					NewAppendFileTool(workspace, restrict, patterns),
				} {
					result := tool.Execute(context.Background(), map[string]any{
						"path": path, "content": "changed", "overwrite": true,
						"old_text": "TOP_SECRET", "new_text": "changed",
					})
					if !result.IsError {
						t.Errorf("write allowed, restrict=%v: %+v", restrict, result)
					}
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "TOP_SECRET" {
				t.Fatalf("credential changed: %q, %v", data, err)
			}
			if target == root {
				result := NewWriteFileTool(workspace, true).Execute(context.Background(), map[string]any{
					"path": filepath.Join(root, "new", "token.txt"), "content": "new token",
				})
				if !result.IsError {
					t.Fatalf("new credential allowed: %+v", result)
				}
			}
		})
	}
}

func TestFileSafetyCaseInsensitivePaths(t *testing.T) {
	workspace := t.TempDir()
	home := filepath.Join(workspace, "AppHome")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	homeAlias := filepath.Join(workspace, "apphome")
	if _, err := os.Stat(homeAlias); os.IsNotExist(err) {
		t.Skip("requires a case-insensitive filesystem")
	} else if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICOCLAW_HOME", home)
	t.Setenv("CODEX_HOME", home)
	for _, name := range []string{"auth.json", "auth/mcp/token.txt", "plugin-data/demo/token.txt", "channels/weixin/context-tokens/account.json", "sessions/chat.jsonl"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(home, filepath.FromSlash(name))
			alias := filepath.Join(homeAlias, filepath.FromSlash(strings.ToUpper(name)))
			if name == "sessions/chat.jsonl" {
				alias = filepath.Join(home, filepath.FromSlash(strings.ToUpper(name)))
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("TOP_SECRET"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, restrict := range []bool{false, true} {
				if name != "sessions/chat.jsonl" {
					result := NewReadFileTool(home, restrict, MaxReadFileSize).Execute(
						context.Background(), map[string]any{"path": alias},
					)
					if !result.IsError {
						t.Errorf("case alias exposed credentials, restrict=%v: %+v", restrict, result)
					}
				}
				for _, tool := range []interface {
					Execute(ctx context.Context, args map[string]any) *ToolResult
				}{
					NewWriteFileTool(home, restrict), NewEditFileTool(home, restrict), NewAppendFileTool(home, restrict),
				} {
					result := tool.Execute(context.Background(), map[string]any{
						"path": alias, "content": "changed", "overwrite": true,
						"old_text": "TOP_SECRET", "new_text": "changed",
					})
					if !result.IsError {
						t.Errorf("case alias write allowed, restrict=%v: %+v", restrict, result)
					}
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "TOP_SECRET" {
				t.Fatalf("protected contents changed: %q, %v", data, err)
			}
		})
	}
	for _, name := range []string{"AUTH.JSON", "AUTH/MCP/new/token.txt", "SESSIONS/new/chat.jsonl"} {
		freshHome := t.TempDir()
		t.Setenv("PICOCLAW_HOME", freshHome)
		result := NewWriteFileTool(freshHome, false).Execute(context.Background(), map[string]any{
			"path": filepath.Join(strings.ToUpper(freshHome), filepath.FromSlash(name)), "content": "new",
		})
		if !result.IsError {
			t.Errorf("new protected file allowed: %s", name)
		}
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{"/ETC/PASSWD", "/ETC/SUDOERS", "/ETC/SYSTEMD/new.service"} {
			if err := checkSensitiveWritePath(path, "", false); err == nil {
				t.Errorf("system case alias allowed: %s", path)
			}
		}
	}
}

func TestProtectedRelativePathDistinctRoots(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, "AppHome")
	other := filepath.Join(workspace, "apphome")
	for _, path := range []string{root, other} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(rootInfo, otherInfo) {
		t.Skip("requires a case-sensitive filesystem")
	}
	if _, within := protectedRelativePath(root, filepath.Join(other, "auth.json")); within {
		t.Fatal("distinct directories must not be treated as the same root")
	}
}
