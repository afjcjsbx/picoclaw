package fstools

import (
	"path/filepath"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// pathSafety is the ParallelSafety of a call that touches a single path. The
// path is resolved like the tools do (workspace-relative, symlinks followed
// through the nearest existing ancestor); anything unresolvable is a barrier.
func pathSafety(workspace, path string, write bool) toolshared.ParallelSafety {
	if path == "" {
		return toolshared.ParallelSafety{}
	}
	if !filepath.IsAbs(path) {
		if workspace == "" {
			return toolshared.ParallelSafety{}
		}
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return toolshared.ParallelSafety{}
	}
	resolved, err := resolvePathAgainstExistingAncestor(abs)
	if err != nil {
		return toolshared.ParallelSafety{}
	}
	return toolshared.ParallelSafety{
		Safe:  true,
		Paths: []toolshared.PathAccess{{Path: resolved, Write: write}},
	}
}

func pathArg(args map[string]any) string {
	path, _ := args["path"].(string)
	return path
}

func (t *ReadFileTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), false)
}

func (t *ReadFileLinesTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), false)
}

func (t *ListDirTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), false)
}

func (t *LoadImageTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), false)
}

func (t *WriteFileTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	path := pathArg(args)
	if path == "" {
		return toolshared.ParallelSafety{}
	}
	return pathSafety("", resolveToolPath(t.fs, path), true)
}

func (t *EditFileTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), true)
}

func (t *AppendFileTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	return pathSafety(t.workspace, pathArg(args), true)
}

// ParallelSafety mirrors Execute: a blank path searches the workspace root.
// The footprint is the whole subtree, so any write below it conflicts.
func (t *SearchFilesTool) ParallelSafety(args map[string]any) toolshared.ParallelSafety {
	path := pathArg(args)
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	return pathSafety(t.workspace, path, false)
}
