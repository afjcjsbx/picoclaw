package fstools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemToolsParallelSafety(t *testing.T) {
	ws := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(ws); err == nil {
		ws = resolved
	}
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink(filepath.Join(ws, "a.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	read := NewReadFileTool(ws, true, 0)
	write := NewWriteFileTool(ws, true)
	search := NewSearchFilesTool(ws, true)

	got := read.ParallelSafety(map[string]any{"path": "a.txt"})
	if !got.Safe || len(got.Paths) != 1 || got.Paths[0].Path != filepath.Join(ws, "a.txt") || got.Paths[0].Write {
		t.Fatalf("read_file relative = %+v", got)
	}

	abs := read.ParallelSafety(map[string]any{"path": filepath.Join(ws, "a.txt")})
	if abs.Paths[0].Path != got.Paths[0].Path {
		t.Errorf("relative and absolute differ: %q vs %q", got.Paths[0].Path, abs.Paths[0].Path)
	}

	viaLink := write.ParallelSafety(map[string]any{"path": "link.txt"})
	if !viaLink.Safe || viaLink.Paths[0].Path != got.Paths[0].Path || !viaLink.Paths[0].Write {
		t.Errorf("write through symlink = %+v, want write on %q", viaLink, got.Paths[0].Path)
	}

	fresh := write.ParallelSafety(map[string]any{"path": "sub/new.txt"})
	if !fresh.Safe || fresh.Paths[0].Path != filepath.Join(ws, "sub", "new.txt") {
		t.Errorf("write to a file that does not exist yet = %+v", fresh)
	}

	root := search.ParallelSafety(map[string]any{})
	if !root.Safe || root.Paths[0].Path != ws || root.Paths[0].Write {
		t.Errorf("search_files default path = %+v, want read on workspace root", root)
	}
}

func TestFilesystemToolsParallelSafety_UnresolvableIsBarrier(t *testing.T) {
	ws := t.TempDir()
	read := NewReadFileTool(ws, true, 0)
	for name, args := range map[string]map[string]any{
		"missing": {},
		"empty":   {"path": ""},
		"number":  {"path": 3},
	} {
		if got := read.ParallelSafety(args); got.Safe {
			t.Errorf("%s: read_file path %v should be a barrier, got %+v", name, args["path"], got)
		}
	}

	noWorkspace := NewReadFileTool("", false, 0)
	if got := noWorkspace.ParallelSafety(map[string]any{"path": "rel.txt"}); got.Safe {
		t.Errorf("relative path without workspace should be a barrier, got %+v", got)
	}
	if got := noWorkspace.ParallelSafety(map[string]any{"path": filepath.Join(ws, "x")}); !got.Safe {
		t.Errorf("absolute path without workspace should resolve, got %+v", got)
	}
}
