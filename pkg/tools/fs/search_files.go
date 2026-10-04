package fstools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxSearchFilesScanned = 5000
	maxSearchFileBytes    = 1024 * 1024
	maxSearchBytesScanned = 64 * 1024 * 1024
	maxSearchResults      = 200
	maxSearchOffset       = 10000
	maxSearchContext      = 20
	maxSearchLineLength   = 240
	maxSearchOutputBytes  = 64 * 1024
)

var (
	errSearchByteLimit = errors.New("search byte limit reached")
	errSearchSkipFile  = errors.New("skip unreadable search file")
)

// SearchFilesTool searches content or filenames within the configured filesystem sandbox.
type SearchFilesTool struct {
	fs fileSystem
}

func NewSearchFilesTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *SearchFilesTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &SearchFilesTool{fs: buildFs(workspace, restrict, patterns)}
}

func (t *SearchFilesTool) Name() string { return "search_files" }

func (t *SearchFilesTool) Description() string {
	return "Search file contents or find files by name. Use this instead of grep/rg/find/ls in terminal. target='content' does regex search with files_only, count, or context output; target='files' finds names by glob. Supports pagination. On macOS, searches above your home directory skip TCC-protected folders; target one directly to include it. Searches the configured readable filesystem sandbox."
}

func (t *SearchFilesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex for content search, or filename glob (for example *.go) for target=files",
			},
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{"content", "files"},
				"description": "Search file contents or filenames",
				"default":     "content",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search (default: .)",
				"default":     ".",
			},
			"file_glob": map[string]any{"type": "string", "description": "Optional filename glob for content search"},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results to return (default: 50, maximum: 200)",
				"default":     50,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Number of results to skip (default: 0)",
				"default":     0,
			},
			"order": map[string]any{
				"type":        "string",
				"enum":        []string{"discovery", "modified"},
				"description": "File search order; modified sorts by newest modification time and scans the full tree",
				"default":     "discovery",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"enum":        []string{"content", "files_only", "count"},
				"description": "Content search output format",
				"default":     "content",
			},
			"context": map[string]any{
				"type":        "integer",
				"description": "Lines before and after each content match (maximum: 20)",
				"default":     0,
			},
		},
		"required": []string{"pattern"},
	}
}

type searchFilesOptions struct {
	pattern    string
	target     string
	path       string
	fileGlob   string
	limit      int
	offset     int
	order      string
	outputMode string
	context    int
}

type searchFileEntry struct {
	path    string
	modTime int64
}

func (t *SearchFilesTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	opts, err := parseSearchFilesOptions(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if opts.target == "files" {
		return t.searchNames(ctx, opts)
	}
	return t.searchContents(ctx, opts)
}

func parseSearchFilesOptions(args map[string]any) (searchFilesOptions, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return searchFilesOptions{}, fmt.Errorf("pattern is required and must be a non-empty string")
	}
	opts := searchFilesOptions{
		pattern:    pattern,
		target:     "content",
		path:       ".",
		limit:      50,
		order:      "discovery",
		outputMode: "content",
	}
	if value, exists := args["target"].(string); exists && value != "" {
		opts.target = value
	}
	if opts.target != "content" && opts.target != "files" {
		return searchFilesOptions{}, fmt.Errorf("target must be 'content' or 'files'")
	}
	if value, exists := args["path"].(string); exists && strings.TrimSpace(value) != "" {
		opts.path = value
	}
	if value, exists := args["file_glob"].(string); exists {
		opts.fileGlob = value
	}
	if value, exists := args["order"].(string); exists && value != "" {
		opts.order = value
	}
	if opts.order != "discovery" && opts.order != "modified" {
		return searchFilesOptions{}, fmt.Errorf("order must be 'discovery' or 'modified'")
	}
	if value, exists := args["output_mode"].(string); exists && value != "" {
		opts.outputMode = value
	}
	if opts.outputMode != "content" && opts.outputMode != "files_only" && opts.outputMode != "count" {
		return searchFilesOptions{}, fmt.Errorf("output_mode must be 'content', 'files_only', or 'count'")
	}
	if opts.target == "content" {
		if _, err := regexp.Compile(opts.pattern); err != nil {
			return searchFilesOptions{}, fmt.Errorf("invalid regex pattern: %w", err)
		}
		if opts.fileGlob != "" {
			if _, err := filepath.Match(opts.fileGlob, "example"); err != nil {
				return searchFilesOptions{}, fmt.Errorf("invalid file_glob: %w", err)
			}
		}
	} else if _, err := filepath.Match(opts.pattern, "example"); err != nil {
		return searchFilesOptions{}, fmt.Errorf("invalid filename glob: %w", err)
	}
	if opts.limit, ok = intArg(args, "limit", opts.limit); !ok || opts.limit < 1 || opts.limit > maxSearchResults {
		return searchFilesOptions{}, fmt.Errorf("limit must be between 1 and %d", maxSearchResults)
	}
	if opts.offset, ok = intArg(args, "offset", 0); !ok || opts.offset < 0 || opts.offset > maxSearchOffset {
		return searchFilesOptions{}, fmt.Errorf("offset must be between 0 and %d", maxSearchOffset)
	}
	if opts.context, ok = intArg(args, "context", 0); !ok || opts.context < 0 || opts.context > maxSearchContext {
		return searchFilesOptions{}, fmt.Errorf("context must be between 0 and %d", maxSearchContext)
	}
	return opts, nil
}

func intArg(args map[string]any, key string, fallback int) (int, bool) {
	value, exists := args[key]
	if !exists {
		return fallback, true
	}
	switch n := value.(type) {
	case int:
		return n, true
	case int64:
		return int(n), int64(int(n)) == n
	case float64:
		return int(n), n == float64(int(n))
	default:
		return 0, false
	}
}

func (t *SearchFilesTool) searchNames(ctx context.Context, opts searchFilesOptions) *ToolResult {
	entries := make([]searchFileEntry, 0, opts.limit+opts.offset)
	requested := opts.offset + opts.limit
	filesScanned, truncated, err := t.walkPath(
		ctx,
		opts.path,
		opts.order == "modified",
		func(path string) (bool, error) {
			if !matchesFileGlob(opts.pattern, filepath.Base(path)) {
				return false, nil
			}
			info, err := t.fs.Stat(path)
			if err != nil {
				return false, errSearchSkipFile
			}
			if !info.Mode().IsRegular() {
				return false, errSearchSkipFile
			}
			entries = append(entries, searchFileEntry{path: path, modTime: info.ModTime().UnixNano()})
			return opts.order == "discovery" && len(entries) > requested, nil
		},
	)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to search %q: %v", opts.path, err))
	}
	if opts.order == "modified" {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].modTime == entries[j].modTime {
				return entries[i].path < entries[j].path
			}
			return entries[i].modTime > entries[j].modTime
		})
	}
	end := min(opts.offset+opts.limit, len(entries))
	if opts.offset >= len(entries) {
		if truncated {
			return NewToolResult(
				fmt.Sprintf("No matching files found in the first %d files scanned; more may exist.", filesScanned),
			)
		}
		return NewToolResult(fmt.Sprintf("No matching files (%d files scanned).", filesScanned))
	}
	paths := make([]string, 0, end-opts.offset)
	for _, entry := range entries[opts.offset:end] {
		paths = append(paths, entry.path)
	}
	if end < len(entries) {
		truncated = true
	}
	if len(paths) == 0 {
		return NewToolResult(fmt.Sprintf("No matching files (%d files scanned).", filesScanned))
	}
	result := strings.Join(paths, "\n")
	if truncated {
		result += "\n[More results are available; use offset to continue.]"
	}
	result, _ = capSearchOutput(result)
	return NewToolResult(fmt.Sprintf("%s\n\n%d file(s), %d files scanned.", result, len(paths), filesScanned))
}

func (t *SearchFilesTool) searchContents(ctx context.Context, opts searchFilesOptions) *ToolResult {
	pattern, _ := regexp.Compile(opts.pattern) // validated in parseSearchFilesOptions
	rows := make([]string, 0, opts.limit+1)
	rowsSeen := 0
	bytesScanned := int64(0)
	var byteLimit bool
	filesScanned, truncated, err := t.walkPath(ctx, opts.path, false, func(path string) (bool, error) {
		if opts.fileGlob != "" && !matchesFileGlob(opts.fileGlob, filepath.Base(path)) {
			return false, nil
		}
		content, size, err := readSearchFile(t.fs, path, maxSearchBytesScanned-bytesScanned)
		if errors.Is(err, errSearchByteLimit) {
			byteLimit = true
			return true, nil
		}
		bytesScanned += size
		if err != nil {
			return false, errSearchSkipFile
		}
		fileRows, _ := formatContentMatches(path, content, pattern, opts, &rowsSeen, opts.limit+1-len(rows))
		rows = append(rows, fileRows...)
		return len(rows) > opts.limit, nil
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to search %q: %v", opts.path, err))
	}
	moreResults := len(rows) > opts.limit
	if moreResults {
		rows = rows[:opts.limit]
	}
	truncated = truncated || byteLimit || moreResults
	if len(rows) == 0 {
		if truncated {
			return NewToolResult("No matches found before the search safety limit (5,000 files or 64 MiB scanned).")
		}
		return NewToolResult(fmt.Sprintf("No matches found (%d files scanned).", filesScanned))
	}
	result := strings.Join(rows, "\n")
	if truncated {
		result += "\n[Search stopped at a safety limit. Use offset to continue when the result limit was reached.]"
	}
	result, _ = capSearchOutput(result)
	return NewToolResult(fmt.Sprintf("%s\n\n%d result(s), %d files scanned.", result, len(rows), filesScanned))
}

func capSearchOutput(value string) (string, bool) {
	if len(value) <= maxSearchOutputBytes {
		return value, false
	}
	cut := maxSearchOutputBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "\n[Output capped at 64 KiB. Use offset to continue.]", true
}

func formatContentMatches(
	path string,
	content []byte,
	pattern *regexp.Regexp,
	opts searchFilesOptions,
	rowsSeen *int,
	remaining int,
) ([]string, int) {
	if len(content) == 0 {
		return nil, 0
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	hits := make([]bool, len(lines))
	count := 0
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
		line = lines[i]
		if pattern.MatchString(line) {
			hits[i] = true
			count++
		}
	}
	if count == 0 {
		return nil, 0
	}
	if opts.outputMode == "files_only" || opts.outputMode == "count" {
		(*rowsSeen)++
		if *rowsSeen <= opts.offset || remaining <= 0 {
			return nil, count
		}
		if opts.outputMode == "files_only" {
			return []string{path}, count
		}
		return []string{fmt.Sprintf("%s: %d", path, count)}, count
	}

	var rows []string
	for i, matched := range hits {
		if !matched {
			continue
		}
		(*rowsSeen)++
		if *rowsSeen <= opts.offset {
			continue
		}
		start, end := max(0, i-opts.context), min(len(lines)-1, i+opts.context)
		var block strings.Builder
		if opts.context > 0 {
			block.WriteString(fmt.Sprintf("%s:\n", path))
		}
		for lineIndex := start; lineIndex <= end; lineIndex++ {
			line := truncateSearchLine(lines[lineIndex])
			if opts.context > 0 {
				prefix := " "
				if hits[lineIndex] {
					prefix = ">"
				}
				block.WriteString(fmt.Sprintf("%s %d: %s\n", prefix, lineIndex+1, line))
			} else {
				block.WriteString(fmt.Sprintf("%s:%d: %s\n", path, lineIndex+1, line))
			}
		}
		rows = append(rows, strings.TrimRight(block.String(), "\n"))
		if len(rows) >= remaining {
			break
		}
	}
	return rows, count
}

func truncateSearchLine(line string) string {
	if len(line) <= maxSearchLineLength {
		return line
	}
	cut := maxSearchLineLength
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut] + "…"
}

func readSearchFile(filesystem fileSystem, path string, remainingBytes int64) ([]byte, int64, error) {
	entry, err := filesystem.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if !entry.Mode().IsRegular() || entry.Size() > maxSearchFileBytes {
		return nil, 0, fmt.Errorf("not a searchable regular file")
	}
	f, err := filesystem.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return nil, 0, fmt.Errorf("not a searchable regular file")
	}
	if info.Size() > remainingBytes {
		return nil, 0, errSearchByteLimit
	}
	content, err := io.ReadAll(io.LimitReader(f, maxSearchFileBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(content) > maxSearchFileBytes {
		return nil, int64(len(content)), fmt.Errorf("file exceeded search size limit")
	}
	if bytesContainNUL(content) || !utf8.Valid(content) {
		return nil, info.Size(), fmt.Errorf("binary file")
	}
	return content, int64(len(content)), nil
}

func bytesContainNUL(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

func (t *SearchFilesTool) walkPath(
	ctx context.Context,
	path string,
	fullTree bool,
	visit func(string) (bool, error),
) (int, bool, error) {
	info, err := t.fs.Stat(path)
	if err != nil {
		return 0, false, err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return 0, false, nil
		}
		stop, err := visit(path)
		if errors.Is(err, errSearchSkipFile) {
			return 1, false, nil
		}
		return 1, stop, err
	}
	filesScanned := 0
	truncated := false
	var walk func(string, bool) error
	walk = func(dir string, isRoot bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := t.fs.ReadDir(dir)
		if err != nil {
			if !isRoot {
				return nil
			}
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			child := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				if shouldSkipSearchDir(path, child, entry.Name()) {
					continue
				}
				if err := walk(child, false); err != nil {
					return err
				}
				if truncated {
					return nil
				}
				continue
			}
			if !entry.Type().IsRegular() {
				continue
			}
			filesScanned++
			if filesScanned > maxSearchFilesScanned && !fullTree {
				truncated = true
				return nil
			}
			stop, err := visit(child)
			if errors.Is(err, errSearchSkipFile) {
				continue
			}
			if err != nil {
				return err
			}
			if stop {
				truncated = true
				return nil
			}
		}
		return nil
	}
	if err := walk(path, true); err != nil {
		return filesScanned, truncated, err
	}
	return filesScanned, truncated, nil
}

func matchesFileGlob(pattern, name string) bool {
	matched, _ := filepath.Match(pattern, name)
	return matched
}

func shouldSkipSearchDir(root, path, name string) bool {
	if name == ".git" {
		return true
	}
	if runtime.GOOS != "darwin" || !isAncestorOfHome(root) {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return false
	}
	return filepath.Dir(absPath) == absHome && isTCCProtectedDir(name)
}

func isAncestorOfHome(root string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absHome)
	return err == nil && rel != "." && filepath.IsLocal(rel)
}

func isTCCProtectedDir(name string) bool {
	switch name {
	case "Desktop", "Documents", "Downloads", "Library", "Movies", "Music", "Pictures":
		return true
	default:
		return false
	}
}
