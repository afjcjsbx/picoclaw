package fileutil

import "os"

// ExpandHome expands a leading ~ or ~/ using the current user's home directory.
// If the home directory cannot be determined, it returns path unchanged.
func ExpandHome(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if path[1] == '/' {
		return home + path[1:]
	}
	return path
}

// Exists reports whether path names an existing non-directory file.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
