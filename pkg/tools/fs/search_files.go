package fstools

import (
	"bufio"
	"bytes"
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
	maxSearchBytesScanned = 64 * 1024 * 1024
	maxSearchResults      = 200
	maxSearchOffset       = 10000
	maxSearchContext      = 20
	maxSearchLineLength   = 240
	maxSearchOutputBytes  = 64 * 1024
	// maxSearchHunkLines bounds the number of lines in one merged context
	// block so a single result row always stays well below the output cap.
	maxSearchHunkLines = 50
	// scanSearchReadBufferBytes is the buffered reader size used to stream
	// file contents line by line.
	scanSearchReadBufferBytes = 64 * 1024
	// maxSearchLineBytes caps the text retained for one line, so a minified
	// file with a multi-megabyte line cannot allocate the whole global budget.
	// Longer lines are truncated before matching.
	maxSearchLineBytes = 1 << 20
	// scanSearchBinarySniffBytes is how much of a file head is inspected for
	// NUL bytes when deciding whether the file is binary.
	scanSearchBinarySniffBytes = 8 * 1024
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
	return "Search file contents or find files by name. Use this instead of grep/rg/find/ls in terminal. target='content' does regex search with files_only, count, or context output; target='files' finds names by glob or substring (an empty pattern lists files). Skipped: paths ignored by .gitignore/.ignore or .git, and binary files (NUL byte in the first 8 KiB); other encodings are searched. Credential-looking tokens in results are replaced with [FILTERED]. Supports pagination. On macOS, searches above your home directory skip TCC-protected folders; target one directly to include it. Searches the configured readable filesystem sandbox."
}

func (t *SearchFilesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex for content search (empty matches every line), or a filename pattern for target=files: a glob such as *.go, src/**/*.ts or *.{js,ts}, or a plain substring such as docker-compose (empty lists every file)",
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
			"file_glob": map[string]any{
				"type":        "string",
				"description": "Optional filename pattern for content search (same syntax as pattern for target=files)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum results to return (default: 50, maximum: 200)",
				"default":     50,
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Number of results to skip (results are match lines, or merged context blocks when context > 0)",
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
				"description": "Lines before and after each content match (maximum: 20); contiguous matches merge into one block",
				"default":     0,
			},
		},
		"required": []string{"pattern"},
	}
}

type searchFilesOptions struct {
	pattern     string
	target      string
	path        string
	fileGlob    string
	namePattern searchPattern
	filePattern searchPattern
	limit       int
	offset      int
	order       string
	outputMode  string
	context     int
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
	if !ok {
		return searchFilesOptions{}, fmt.Errorf("pattern is required and must be a string")
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
			matcher, err := newSearchPattern(opts.fileGlob)
			if err != nil {
				return searchFilesOptions{}, fmt.Errorf("invalid file_glob: %w", err)
			}
			opts.filePattern = matcher
		}
	} else {
		matcher, err := newSearchPattern(opts.pattern)
		if err != nil {
			return searchFilesOptions{}, fmt.Errorf("invalid filename pattern: %w", err)
		}
		opts.namePattern = matcher
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
			if !opts.namePattern.match(relativeSearchPath(opts.path, path)) {
				return false, nil
			}
			// Only the modified order needs per-file metadata; in discovery
			// order the walk's directory entries (already checked as regular
			// files) are enough, so no extra stat is issued per file.
			entry := searchFileEntry{path: path}
			if opts.order == "modified" {
				info, err := t.fs.Stat(path)
				if err != nil {
					return false, errSearchSkipFile
				}
				if !info.Mode().IsRegular() {
					return false, errSearchSkipFile
				}
				entry.modTime = info.ModTime().UnixNano()
			}
			entries = append(entries, entry)
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
	result, shown, outputCapped := capSearchRows(paths)
	if shown == 0 {
		return ErrorResult("a single result exceeds the search output limit; search a narrower path")
	}
	if outputCapped {
		result += fmt.Sprintf("\n[Output capped at 64 KiB; use offset=%d to continue.]", opts.offset+shown)
	} else if truncated {
		result += "\n[More results are available; use offset to continue.]"
	}
	return NewToolResult(fmt.Sprintf("%s\n\n%d file(s), %d files scanned.", result, shown, filesScanned))
}

func (t *SearchFilesTool) searchContents(ctx context.Context, opts searchFilesOptions) *ToolResult {
	pattern, _ := regexp.Compile(opts.pattern) // validated in parseSearchFilesOptions
	rows := make([]string, 0, opts.limit+1)
	rowsSeen := 0
	bytesScanned := int64(0)
	var byteLimit bool
	filesScanned, truncated, err := t.walkPath(ctx, opts.path, false, func(path string) (bool, error) {
		if opts.fileGlob != "" && !opts.filePattern.match(relativeSearchPath(opts.path, path)) {
			return false, nil
		}
		fileResult, err := t.scanSearchFile(
			path, pattern, opts, &rowsSeen, opts.limit+1-len(rows), maxSearchBytesScanned-bytesScanned,
		)
		if err != nil {
			return false, errSearchSkipFile
		}
		bytesScanned += fileResult.bytesRead
		if fileResult.byteLimit {
			// The file does not fit in the remaining scan budget: skip it
			// and keep walking, so smaller files that still fit are reached.
			byteLimit = true
		}
		rows = append(rows, fileResult.rows...)
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
	result, shown, outputCapped := capSearchRows(rows)
	if shown == 0 {
		return ErrorResult("a single result exceeds the search output limit; reduce context")
	}
	if outputCapped {
		result += fmt.Sprintf("\n[Output capped at 64 KiB; use offset=%d to continue.]", opts.offset+shown)
	} else if truncated {
		result += "\n[Search stopped at a safety limit. Use offset to continue when the result limit was reached.]"
	}
	return NewToolResult(fmt.Sprintf("%s\n\n%d result(s), %d files scanned.", result, shown, filesScanned))
}

func capSearchRows(rows []string) (string, int, bool) {
	const footerBytes = 256
	var result strings.Builder
	shown := 0
	for _, row := range rows {
		separator := 0
		if shown > 0 {
			separator = 1
		}
		if result.Len()+separator+len(row) > maxSearchOutputBytes-footerBytes {
			return result.String(), shown, true
		}
		if separator > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(row)
		shown++
	}
	return result.String(), shown, false
}

// searchLine is one scanned line: its 1-based number, its text, and the
// match position when the pattern matches (nil otherwise).
type searchLine struct {
	number int
	text   string
	loc    []int
}

func (l searchLine) hit() bool { return l.loc != nil }

// searchFileResult carries the outcome of scanning one file.
type searchFileResult struct {
	rows      []string
	matches   int
	bytesRead int64
	byteLimit bool
}

// scanSearchFile streams one file line by line and formats its matches as
// result rows, reading at most remainingBytes of content and never holding
// the whole file in memory. When the file cannot be scanned in full because
// the global budget ran out mid-read, byteLimit is set and the partial
// results are discarded, as if the file had not fit at all. A file is
// treated as binary, and skipped silently, when its first bytes contain a
// NUL byte; invalid UTF-8 is not a reason to skip, so text in other
// encodings is searched as byte text. Rendered lines pass through
// redactSearchSecrets, so credential-looking values in results are masked
// (see redact.go).
func (t *SearchFilesTool) scanSearchFile(
	path string,
	pattern *regexp.Regexp,
	opts searchFilesOptions,
	rowsSeen *int,
	remainingRows int,
	remainingBytes int64,
) (searchFileResult, error) {
	var result searchFileResult
	// A single stat decides type and budget eligibility before opening; a
	// file that grows past the budget after this is caught by budgetReader.
	entry, err := t.fs.Stat(path)
	if err != nil {
		return result, err
	}
	if !entry.Mode().IsRegular() {
		return result, errors.New("not a searchable regular file")
	}
	if entry.Size() > remainingBytes {
		result.byteLimit = true
		return result, nil
	}
	f, err := t.fs.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()

	br := &budgetReader{reader: f, limit: remainingBytes}
	reader := bufio.NewReaderSize(br, scanSearchReadBufferBytes)

	// Like ripgrep, binary detection inspects only the head of the file: a
	// NUL byte there marks it as binary. Invalid UTF-8 is not a reason to
	// skip, so Latin-1 and other encodings are searched as byte text.
	sniff, sniffErr := reader.Peek(scanSearchBinarySniffBytes)
	switch {
	case errors.Is(sniffErr, errSearchByteLimit):
		result.byteLimit = true
		result.bytesRead = br.read
		return result, nil
	case sniffErr != nil && !errors.Is(sniffErr, io.EOF):
		return result, sniffErr
	case bytes.IndexByte(sniff, 0) >= 0:
		result.bytesRead = br.read
		return result, nil
	}

	var (
		rows   []string
		seen   int // rows generated for this file, committed to rowsSeen when the file is scanned in full
		number int
		done   bool

		hunkOpen bool
		lastHit  int
		nextEmit int
		buf      []searchLine
		lookback []searchLine
	)

	emitChunk := func(lines []searchLine) {
		seen++
		if *rowsSeen+seen <= opts.offset {
			return
		}
		var block strings.Builder
		block.WriteString(path)
		block.WriteString(":\n")
		for _, line := range lines {
			prefix := " "
			if line.hit() {
				prefix = ">"
			}
			rendered := renderSearchLine(line, pattern)
			block.WriteString(fmt.Sprintf("%s %d: %s\n", prefix, line.number, rendered))
		}
		rows = append(rows, strings.TrimRight(block.String(), "\n"))
		if len(rows) >= remainingRows {
			done = true
		}
	}

	emitMatchLine := func(line searchLine) {
		seen++
		if *rowsSeen+seen <= opts.offset {
			return
		}
		rendered := renderSearchLine(line, pattern)
		rows = append(rows, fmt.Sprintf("%s:%d: %s", path, line.number, rendered))
		if len(rows) >= remainingRows {
			done = true
		}
	}

	pushRing := func(line searchLine) {
		lookback = append(lookback, line)
		if len(lookback) > opts.context {
			lookback = lookback[len(lookback)-opts.context:]
		}
	}

	flushChunks := func() {
		for !done && len(buf) >= maxSearchHunkLines && nextEmit+maxSearchHunkLines-1 <= lastHit+opts.context {
			emitChunk(buf[:maxSearchHunkLines])
			buf = buf[maxSearchHunkLines:]
			nextEmit += maxSearchHunkLines
		}
	}

	closeHunk := func() {
		confirmed := min(len(buf), lastHit+opts.context-nextEmit+1)
		for confirmed > 0 && !done {
			chunk := min(confirmed, maxSearchHunkLines)
			emitChunk(buf[:chunk])
			buf = buf[chunk:]
			nextEmit += chunk
			confirmed -= chunk
		}
		// The lines left after the confirmed hunk sit past the merge
		// window: they can only serve as leading context for a later hunk,
		// so drop the oldest one.
		if len(buf) > 1 {
			buf = buf[1:]
		} else {
			buf = buf[:0]
		}
		lookback = append(lookback[:0], buf...)
		buf = buf[:0]
		hunkOpen = false
	}

	for !done {
		line, _, err := readSearchLine(reader, maxSearchLineBytes)
		if err != nil {
			if errors.Is(err, errSearchByteLimit) {
				result.byteLimit = true
				break
			}
			if !errors.Is(err, io.EOF) {
				return result, err
			}
		}
		if line != "" {
			number++
			text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			current := searchLine{number: number, text: text, loc: pattern.FindStringIndex(text)}
			if current.hit() {
				result.matches++
			}
			switch {
			case opts.outputMode == "files_only" || opts.outputMode == "count":
				// Counting only; the row is built after the scan.
			case opts.context == 0:
				if current.hit() {
					emitMatchLine(current)
				}
			case current.hit():
				if hunkOpen {
					buf = append(buf, current)
					lastHit = number
				} else {
					buf = append(buf[:0], lookback...)
					buf = append(buf, current)
					lookback = lookback[:0]
					hunkOpen = true
					lastHit = number
					nextEmit = buf[0].number
				}
			case !hunkOpen:
				pushRing(current)
			case number < lastHit+2*opts.context+1:
				buf = append(buf, current)
			default:
				closeHunk()
				pushRing(current)
			}
			if opts.context > 0 {
				flushChunks()
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	result.bytesRead = br.read
	if result.byteLimit {
		// The file exceeded the remaining budget mid-read: drop its results.
		result.matches = 0
		return result, nil
	}
	if !done && hunkOpen && opts.context > 0 {
		confirmed := min(len(buf), lastHit+opts.context-nextEmit+1)
		if confirmed > 0 {
			emitChunk(buf[:confirmed])
		}
	}
	if opts.outputMode != "content" && result.matches > 0 {
		seen++
		if *rowsSeen+seen > opts.offset {
			if opts.outputMode == "files_only" {
				rows = append(rows, path)
			} else {
				rows = append(rows, fmt.Sprintf("%s: %d", path, result.matches))
			}
		}
	}
	*rowsSeen += seen
	result.rows = rows
	return result, nil
}

// truncateSearchLine bounds a line to maxSearchLineLength bytes. When
// matchLoc places the match beyond the cut, the window starts shortly before
// the match instead of at the line start, so the matched text stays visible;
// "…" marks both cut sides.
// renderSearchLine redacts credentials before truncation, so a secret crossing
// the output window is masked in full instead of leaking a partial token. The
// match position is recomputed on the redacted text because masking shifts
// byte offsets.
func renderSearchLine(line searchLine, pattern *regexp.Regexp) string {
	text := redactSearchSecrets(line.text)
	loc := line.loc
	if loc != nil {
		if found := pattern.FindStringIndex(text); found != nil {
			loc = found
		} else if loc[0] > len(text) {
			loc = nil
		}
	}
	return truncateSearchLine(text, loc)
}

// readSearchLine reads one line, retaining at most limit bytes. Longer lines
// are truncated and the rest of the line is drained, so line numbers stay
// aligned. The error follows bufio.Reader semantics: io.EOF on the last line,
// or errSearchByteLimit when the global byte budget runs out.
func readSearchLine(r *bufio.Reader, limit int) (string, bool, error) {
	var buf []byte
	truncated := false
	for {
		frag, err := r.ReadSlice('\n')
		if room := limit - len(buf); room > 0 {
			keep := min(len(frag), room)
			buf = append(buf, frag[:keep]...)
			if keep < len(frag) {
				truncated = true
			}
		} else if len(frag) > 0 {
			truncated = true
		}
		if err == nil {
			return string(buf), truncated, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return string(buf), truncated, err
	}
}

func truncateSearchLine(line string, matchLoc []int) string {
	if len(line) <= maxSearchLineLength {
		return line
	}
	start := 0
	if matchLoc != nil && matchLoc[0] > maxSearchLineLength {
		start = matchLoc[0] - maxSearchLineLength/3
		for start > 0 && !utf8.RuneStart(line[start]) {
			start--
		}
	}
	end := start + maxSearchLineLength
	if end > len(line) {
		end = len(line)
	}
	for end > start+1 && end < len(line) && !utf8.RuneStart(line[end]) {
		end--
	}
	window := line[start:end]
	if start > 0 {
		window = "…" + window
	}
	if end < len(line) {
		window += "…"
	}
	return window
}

// budgetReader tracks how many bytes have been scanned and stops with
// errSearchByteLimit once the remaining budget is exhausted, so one large
// file cannot consume more than the search's global content budget.
type budgetReader struct {
	reader io.Reader
	limit  int64
	read   int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if n > 0 {
		b.read += int64(n)
		if b.read > b.limit {
			excess := b.read - b.limit
			if excess > int64(n) {
				excess = int64(n)
			}
			b.read = b.limit
			return n - int(excess), errSearchByteLimit
		}
	}
	return n, err
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
	var walk func(string, bool, searchIgnoreStack) error
	walk = func(dir string, isRoot bool, ignore searchIgnoreStack) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if set, ok := loadSearchIgnoreSet(t.fs, dir); ok {
			ignore = append(ignore[:len(ignore):len(ignore)], set)
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
				if shouldSkipSearchDir(path, child, entry.Name()) || ignore.ignored(child, true) {
					continue
				}
				if err := walk(child, false, ignore); err != nil {
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
			if ignore.ignored(child, false) {
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
	if err := walk(path, true, nil); err != nil {
		return filesScanned, truncated, err
	}
	return filesScanned, truncated, nil
}

// relativeSearchPath returns path relative to the search root for pattern
// matching. When the search root is the candidate itself (a single-file
// search), the base name is returned instead.
func relativeSearchPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return filepath.Base(path)
	}
	return rel
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
