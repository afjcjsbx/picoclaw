package index

import (
	"os"
	"path/filepath"
	"strings"
)

// allowedExtensions lists file extensions accepted for indexing.
var allowedExtensions = map[string]bool{
	".md": true,
}

// ignoredDirs lists directories to skip during recursive discovery.
var ignoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".pnpm-store":  true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
	"__pycache__":  true,
}

// DiscoveredFile represents a file found during discovery.
type DiscoveredFile struct {
	Path   string // relative path to workspace
	Source string // "memory" or "sessions"
}

// DiscoverMemoryFiles scans the workspace for indexable memory files.
// Discovery order: MEMORY.md, memory.md (fallback), memory/ recursively, extraPaths.
// Symlinks are rejected, files are deduplicated by real path.
func DiscoverMemoryFiles(workspaceDir string, extraPaths []string) ([]DiscoveredFile, error) {
	seen := make(map[string]bool)
	var files []DiscoveredFile

	add := func(absPath, relPath, source string) {
		// Check if the file itself is a symlink (not its parent dirs)
		info, err := os.Lstat(absPath)
		if err != nil {
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return
		}
		// Resolve for deduplication
		real, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return
		}
		if seen[real] {
			return
		}
		ext := strings.ToLower(filepath.Ext(absPath))
		if !allowedExtensions[ext] {
			return
		}
		seen[real] = true
		files = append(files, DiscoveredFile{Path: relPath, Source: source})
	}

	// 1. MEMORY.md
	memoryMD := filepath.Join(workspaceDir, "MEMORY.md")
	memoryMDLower := filepath.Join(workspaceDir, "memory.md")

	if fileExists(memoryMD) {
		add(memoryMD, "MEMORY.md", "memory")
	} else if fileExists(memoryMDLower) {
		// 2. memory.md as fallback
		add(memoryMDLower, "memory.md", "memory")
	}

	// 3. memory/ recursively
	memoryDir := filepath.Join(workspaceDir, "memory")
	if dirExists(memoryDir) {
		err := filepath.WalkDir(memoryDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if d.IsDir() {
				if ignoredDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			// Check for symlinks
			info, err := d.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			rel, err := filepath.Rel(workspaceDir, path)
			if err != nil {
				return nil
			}
			add(path, rel, "memory")
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// 4. extraPaths
	for _, ep := range extraPaths {
		absEP := ep
		if !filepath.IsAbs(ep) {
			absEP = filepath.Join(workspaceDir, ep)
		}
		info, err := os.Stat(absEP)
		if err != nil {
			continue
		}
		if info.IsDir() {
			err := filepath.WalkDir(absEP, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if d.IsDir() {
					if ignoredDirs[d.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				di, err := d.Info()
				if err != nil || di.Mode()&os.ModeSymlink != 0 {
					return nil
				}
				rel, err := filepath.Rel(workspaceDir, path)
				if err != nil {
					// path outside workspace, use absolute
					rel = path
				}
				add(path, rel, "memory")
				return nil
			})
			if err != nil {
				continue
			}
		} else {
			rel, err := filepath.Rel(workspaceDir, absEP)
			if err != nil {
				rel = absEP
			}
			add(absEP, rel, "memory")
		}
	}

	return files, nil
}

// IsMemoryPath checks if a relative path belongs to the memory workspace.
func IsMemoryPath(relPath string) bool {
	if relPath == "MEMORY.md" || relPath == "memory.md" {
		return true
	}
	return strings.HasPrefix(relPath, "memory/") || strings.HasPrefix(relPath, "memory\\")
}

// IsAllowedReadPath checks if a path is within the allowed memory boundaries.
func IsAllowedReadPath(relPath string, extraPaths []string) bool {
	if IsMemoryPath(relPath) {
		return true
	}
	for _, ep := range extraPaths {
		if strings.HasPrefix(relPath, ep) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
