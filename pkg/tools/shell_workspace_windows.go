//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

// absolutePathPattern matches absolute file paths in commands. It is only used
// on Windows, where the POSIX AST workspace pass is unavailable.
var absolutePathPattern = regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)

// guardWindowsWorkspacePaths applies the byte-level workspace check used by
// PowerShell commands. The POSIX AST pass cannot parse this shell, so Windows
// keeps the regex scanner as its workspace boundary layer.
func (t *ExecTool) guardWindowsWorkspacePaths(command, cwdPath string) string {
	cmd := expandPowerShellEnvVars(command)
	if home, err := os.UserHomeDir(); err == nil {
		cmd = strings.ReplaceAll(cmd, "~", filepath.FromSlash(home))
	}

	// Web URL schemes whose path components (starting with //) should be exempt
	// from workspace sandbox checks. file: is intentionally excluded so that
	// file:// URIs are still validated against the workspace boundary.
	webSchemes := []string{"http:", "https:", "ftp:", "ftps:", "sftp:", "ssh:", "git:"}

	matchIndices := absolutePathPattern.FindAllStringIndex(cmd, -1)
	for _, loc := range matchIndices {
		raw := cmd[loc[0]:loc[1]]

		// Skip URL path components that look like they're from web URLs.
		// When a URL like "https://github.com" is parsed, the regex captures
		// "//github.com" as a match (the path portion after "https:").
		if strings.HasPrefix(raw, "//") && loc[0] > 0 {
			before := cmd[:loc[0]]
			isWebURL := false
			for _, scheme := range webSchemes {
				if strings.HasSuffix(before, scheme) {
					isWebURL = true
					break
				}
			}
			if isWebURL {
				continue
			}
		}

		// Skip scheme-less URL paths like "wttr.in/Beijing". A local entry
		// with the same name still goes through full validation (#2965).
		if loc[0] > 0 && raw[0] == '/' {
			j := loc[0] - 1
			for j >= 0 && !isShellTokenBoundary(cmd[j]) {
				j--
			}
			token := cmd[j+1 : loc[0]]
			if looksLikeDomain(token) && !localPathExists(cwdPath, token) {
				continue
			}
		}

		// A regex match can contain several tokens when shell separators are
		// not surrounded by whitespace ("2>/dev/null;echo b").
		for _, pathText := range commandPathTextsFromMatch(cmd, loc[0], loc[1]) {
			p, err := commandPathAbs(pathText, cwdPath)
			if err != nil {
				continue
			}

			// Strip extended-length prefix and NTFS alternate data streams
			// (only when the colon is not the drive letter).
			p = strings.TrimPrefix(p, `\\?\`)
			if idx := strings.Index(p, ":"); idx > 1 {
				p = p[:idx]
			}

			// Resolve symlinks and junctions against the nearest existing
			// ancestor so a new file behind a symlinked directory is checked.
			if resolved, resolveErr := fstools.ResolvePathAgainstExistingAncestor(p); resolveErr == nil {
				p = resolved
			}

			if safePaths[p] {
				continue
			}
			if fstools.IsAllowedPath(p, t.allowedPathPatterns) {
				continue
			}

			if !fstools.IsWithinWorkspace(p, cwdPath) {
				return "Command blocked by safety guard (path outside working dir)"
			}
		}
	}
	return ""
}

// expandPowerShellEnvVars expands environment variable syntax used by both
// PowerShell ($env:VAR) and CMD (%VAR%) to their actual values.
func expandPowerShellEnvVars(cmd string) string {
	// Handle PowerShell style: $env:VAR and ${env:VAR}
	rePs := regexp.MustCompile(`\$\{?env:(\w+)\}?`)
	cmd = rePs.ReplaceAllStringFunc(cmd, func(match string) string {
		varName := rePs.FindStringSubmatch(match)[1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match
	})

	// Handle CMD style: %VAR%
	reCmd := regexp.MustCompile(`%([^%]+)%`)
	return reCmd.ReplaceAllStringFunc(cmd, func(match string) string {
		varName := reCmd.FindStringSubmatch(match)[1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match
	})
}

func commandPathAbs(pathText, cwdPath string) (string, error) {
	if filepath.IsAbs(pathText) {
		return filepath.Abs(pathText)
	}
	return filepath.Abs(filepath.Join(cwdPath, pathText))
}

func commandPathTextsFromMatch(cmd string, start, end int) []string {
	return splitShellPathMatch(commandPathTextFromMatchRaw(cmd, start, end))
}

// shellPathSeparators are bytes that end a path token in shell syntax.
const shellPathSeparators = ";|&<>`"

// splitShellPathMatch splits text that absolutePathPattern swallows because the
// regex only stops at whitespace or quotes. For "2>/dev/null;echo b" the match
// is "/dev/null;echo", which is not in safePaths and would be reported as a
// path outside the working directory (issue #435). Splitting on shell
// separators keeps the safe pseudo-device while the rest is still validated:
// "/dev/null;cat</etc/shadow" also yields "/etc/shadow".
func splitShellPathMatch(pathText string) []string {
	if !strings.HasPrefix(pathText, "/") {
		return []string{pathText}
	}

	segments := strings.FieldsFunc(pathText, func(r rune) bool {
		return strings.ContainsRune(shellPathSeparators, r)
	})
	paths := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment = strings.TrimRight(segment, ",)]}:"); segment != "" {
			paths = append(paths, segment)
		}
	}
	if len(paths) == 0 {
		return []string{pathText}
	}
	return paths
}

func commandPathTextFromMatchRaw(cmd string, start, end int) string {
	raw := cmd[start:end]
	if !strings.HasPrefix(raw, "/") || isUnixAbsolutePathMatchStart(cmd, start) {
		return raw
	}

	tokenStart, tokenEnd := shellTokenBounds(cmd, start)
	prefix := cmd[tokenStart:start]
	// For --flag=rel/path, validate the value. For ambiguous attached option
	// forms like -isystem/path, keep the slash-starting path conservative.
	if eq := strings.IndexByte(prefix, '='); eq >= 0 {
		return cmd[tokenStart+eq+1 : tokenEnd]
	}
	if strings.HasPrefix(prefix, "-") {
		return raw
	}
	return cmd[tokenStart:tokenEnd]
}

func shellTokenBounds(cmd string, idx int) (int, int) {
	start := idx
	for start > 0 && !isShellTokenBoundary(cmd[start-1]) {
		start--
	}
	end := idx
	for end < len(cmd) && !isShellTokenBoundary(cmd[end]) {
		end++
	}
	return start, end
}

// isUnixAbsolutePathMatchStart returns true when a regex match beginning with
// "/" is actually an absolute path token, not the separator inside a relative
// path such as "skills/foo.py".
func isUnixAbsolutePathMatchStart(cmd string, idx int) bool {
	if idx <= 0 {
		return true
	}

	prev := cmd[idx-1]
	if isShellTokenBoundary(prev) || prev == '=' || prev == ',' || prev == '(' || prev == '[' || prev == '{' {
		return true
	}

	j := idx - 1
	for j >= 0 && !isShellTokenBoundary(cmd[j]) {
		j--
	}
	prefix := cmd[j+1 : idx]

	return strings.HasPrefix(prefix, "-") && !strings.Contains(prefix, "=")
}

// isShellTokenBoundary returns true when b is a byte that separates tokens in
// a shell command (space, tab, colon, semicolon, pipe, etc.).
func isShellTokenBoundary(b byte) bool {
	switch b {
	case ' ', '\t', ':', ';', '|', '&', '<', '>', '\'', '"', '`', '\n', '\r':
		return true
	}
	return false
}

// looksLikeDomain returns true when s looks like a DNS domain name: it contains
// at least one dot, starts with an alphanumeric character, and does not end
// with a common file extension.
func looksLikeDomain(s string) bool {
	if len(s) < 3 || !strings.ContainsRune(s, '.') {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	// Exclude tokens ending with common file/programming extensions,
	// e.g. "script.py", "main.go", "app.exe".
	if idx := strings.LastIndexByte(s, '.'); idx >= 0 {
		ext := strings.ToLower(s[idx+1:])
		if commonFileExtension(ext) {
			return false
		}
	}
	return true
}

// commonFileExtension returns true when ext is a file extension that strongly
// indicates a local file rather than a domain TLD.
func commonFileExtension(ext string) bool {
	switch ext {
	case "py", "js", "ts", "tsx", "jsx", "go", "rs", "rb", "php",
		"java", "c", "cpp", "h", "hpp", "cs", "swift", "kt", "scala",
		"sh", "bash", "zsh", "fish", "ps1", "bat", "cmd",
		"txt", "md", "rst", "log", "json", "yaml", "yml", "toml",
		"xml", "html", "css", "scss", "ini", "cfg", "conf", "env",
		"exe", "dll", "so", "dylib", "lib", "a", "o", "obj",
		"zip", "tar", "gz", "bz2", "xz", "7z", "rar",
		"png", "jpg", "jpeg", "gif", "svg", "ico", "bmp", "webp",
		"mp3", "mp4", "wav", "avi", "mov", "mkv", "flac",
		"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx",
		"pub", "pem", "key", "crt", "cer", "p12", "pfx",
		"bak", "tmp", "swp", "lock",
		"ttf", "otf", "woff", "woff2", "eot",
		"deb", "rpm", "apk", "msi", "dmg",
		"sql", "sqlite", "db":
		return true
	}
	return false
}

// localPathExists returns true when the given token resolves to an existing
// filesystem entry relative to cwd.
func localPathExists(cwd, token string) bool {
	info, err := os.Lstat(filepath.Join(cwd, token))
	return err == nil && info != nil
}
