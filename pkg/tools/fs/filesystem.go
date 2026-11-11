package fstools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	MaxReadFileSize = 64 * 1024 // 64KB limit to avoid context overflow

	// DefaultReadFileMaxLines caps how many lines one line-mode read returns
	// when the caller does not request a smaller batch.
	DefaultReadFileMaxLines = 2000
	// DefaultReadFileMaxLineLength caps how long a single line may be in the
	// line-numbered view before the remainder is replaced by a marker.
	DefaultReadFileMaxLineLength = 2000
	// lineTruncationMarker replaces the part of an oversized line that was cut.
	lineTruncationMarker = " ... [truncated]"
	// lineOutputPreallocLimit caps the output builder preallocation in line
	// mode. Pre-sizing from the file size would allocate the whole read budget
	// even when the caller asks for a handful of lines, so the hint is capped
	// at this many bytes.
	lineOutputPreallocLimit = 8 * 1024
)

func ValidatePathWithAllowPaths(
	path, workspace string,
	restrict bool,
	patterns []*regexp.Regexp,
) (string, error) {
	return validatePathWithAllowPaths(path, workspace, restrict, patterns)
}

// ValidateReadablePathWithAllowPaths applies the filesystem tools' read policy
// after the usual workspace and symlink validation.
func ValidateReadablePathWithAllowPaths(
	path, workspace string,
	restrict bool,
	patterns []*regexp.Regexp,
) (string, error) {
	resolved, err := validatePathWithAllowPaths(path, workspace, restrict, patterns)
	if err != nil {
		return "", err
	}
	if err := checkSensitiveReadPath(resolved, "", false); err != nil {
		return "", err
	}
	return resolved, nil
}

func IsAllowedPath(path string, patterns []*regexp.Regexp) bool {
	return isAllowedPath(path, patterns)
}

func validatePathWithAllowPaths(
	path, workspace string,
	restrict bool,
	patterns []*regexp.Regexp,
) (string, error) {
	if err := checkNTNamespacePath(path); err != nil {
		return "", err
	}
	if workspace == "" {
		return path, fmt.Errorf("workspace is not defined")
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath, err = filepath.Abs(filepath.Join(absWorkspace, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve file path: %w", err)
		}
	}

	if restrict {
		if isAllowedPath(absPath, patterns) {
			return absPath, nil
		}

		if !isWithinWorkspace(absPath, absWorkspace) {
			return "", fmt.Errorf("access denied: path is outside the workspace")
		}

		var resolved string
		workspaceReal := absWorkspace
		if resolved, err = filepath.EvalSymlinks(absWorkspace); err == nil {
			workspaceReal = resolved
		}

		if resolved, err = filepath.EvalSymlinks(absPath); err == nil {
			if !isWithinWorkspace(resolved, workspaceReal) {
				return "", fmt.Errorf("access denied: symlink resolves outside workspace")
			}
		} else if os.IsNotExist(err) {
			var parentResolved string
			if parentResolved, err = resolveExistingAncestor(filepath.Dir(absPath)); err == nil {
				if !isWithinWorkspace(parentResolved, workspaceReal) {
					return "", fmt.Errorf("access denied: symlink resolves outside workspace")
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to resolve path: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}

	return absPath, nil
}

func isAllowedPath(path string, patterns []*regexp.Regexp) bool {
	if len(patterns) == 0 {
		return false
	}

	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return false
	}
	if !matchesAllowedPath(cleaned, patterns) {
		return false
	}

	resolved, err := resolvePathAgainstExistingAncestor(cleaned)
	if err != nil {
		return false
	}

	return matchesAllowedPath(resolved, patterns)
}

func matchesAllowedPath(path string, patterns []*regexp.Regexp) bool {
	cleaned := filepath.Clean(path)
	for _, pattern := range patterns {
		if pattern.MatchString(cleaned) {
			return true
		}
		if root, ok := extractAllowedPathRoot(pattern); ok && isWithinAllowedRoot(cleaned, root) {
			return true
		}
	}
	return false
}

func extractAllowedPathRoot(pattern *regexp.Regexp) (string, bool) {
	raw := pattern.String()
	if !strings.HasPrefix(raw, "^") {
		return "", false
	}

	literal := strings.TrimPrefix(raw, "^")

	// Recognize the common "directory prefix" form: ^<literal>(?:/|$)
	literal = strings.TrimSuffix(literal, "(?:/|$)")
	literal = strings.TrimSuffix(literal, `(?:\\|$)`)

	// Reject patterns that still contain regex operators after removing the
	// optional anchored-directory suffix. That keeps arbitrary regex behavior
	// unchanged and only enables normalized prefix matching for literal paths.
	if containsUnescapedRegexMeta(literal) {
		return "", false
	}

	unescaped, ok := unescapeRegexLiteral(literal)
	if !ok || unescaped == "" {
		return "", false
	}

	return filepath.Clean(unescaped), filepath.IsAbs(unescaped)
}

func appendUniquePath(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func containsUnescapedRegexMeta(s string) bool {
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '|':
			return true
		}
	}
	return escaped
}

func unescapeRegexLiteral(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))

	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}

	if escaped {
		return "", false
	}

	return b.String(), true
}

func isWithinAllowedRoot(path, root string) bool {
	candidate := filepath.Clean(path)
	allowedVariants := []string{filepath.Clean(root)}

	if resolvedRoot, err := resolvePathAgainstExistingAncestor(root); err == nil {
		allowedVariants = appendUniquePath(allowedVariants, filepath.Clean(resolvedRoot))
	}

	for _, allowedRoot := range allowedVariants {
		if isWithinWorkspace(candidate, allowedRoot) {
			return true
		}
	}

	return false
}

func resolveExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

// ResolvePathAgainstExistingAncestor resolves symlinks in path, falling back to
// the nearest existing ancestor when the final component does not exist. This
// keeps checks meaningful for paths that are about to be created, where a plain
// filepath.EvalSymlinks would fail and leave the lexical path unchecked.
func ResolvePathAgainstExistingAncestor(path string) (string, error) {
	return resolvePathAgainstExistingAncestor(path)
}

func resolvePathAgainstExistingAncestor(path string) (string, error) {
	cleaned := filepath.Clean(path)
	for current := cleaned; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			suffix, relErr := filepath.Rel(current, cleaned)
			if relErr != nil {
				return "", relErr
			}
			if suffix == "." {
				return filepath.Clean(resolved), nil
			}
			return filepath.Clean(filepath.Join(resolved, suffix)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

type ReadFileTool struct {
	fs        fileSystem
	workspace string
	maxSize   int64
}

type ReadFileLinesTool struct {
	fs            fileSystem
	workspace     string
	maxSize       int64
	maxLines      int
	maxLineLength int
}

// ReadFileAutoTool is the default read_file implementation: it returns
// line-numbered output for text files and falls back to raw byte output for
// binary files. The caller can force byte mode by passing offset or length.
type ReadFileAutoTool struct {
	bytes     *ReadFileTool
	lines     *ReadFileLinesTool
	workspace string
}

func NewReadFileTool(
	workspace string,
	restrict bool,
	maxReadFileSize int,
	allowPaths ...[]*regexp.Regexp,
) *ReadFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}

	maxSize := int64(maxReadFileSize)
	if maxSize <= 0 {
		maxSize = MaxReadFileSize
	}

	return &ReadFileTool{
		fs:        buildFs(workspace, restrict, patterns),
		workspace: workspace,
		maxSize:   maxSize,
	}
}

func NewReadFileLinesTool(
	workspace string,
	restrict bool,
	maxReadFileSize int,
	maxLines int,
	maxLineLength int,
	allowPaths ...[]*regexp.Regexp,
) *ReadFileLinesTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}

	maxSize := int64(maxReadFileSize)
	if maxSize <= 0 {
		maxSize = MaxReadFileSize
	}
	if maxLines <= 0 {
		maxLines = DefaultReadFileMaxLines
	}
	if maxLineLength <= 0 {
		maxLineLength = DefaultReadFileMaxLineLength
	}

	return &ReadFileLinesTool{
		fs:            buildFs(workspace, restrict, patterns),
		workspace:     workspace,
		maxSize:       maxSize,
		maxLines:      maxLines,
		maxLineLength: maxLineLength,
	}
}

func NewReadFileAutoTool(
	workspace string,
	restrict bool,
	maxReadFileSize int,
	maxLines int,
	maxLineLength int,
	allowPaths ...[]*regexp.Regexp,
) *ReadFileAutoTool {
	return &ReadFileAutoTool{
		bytes:     NewReadFileTool(workspace, restrict, maxReadFileSize, allowPaths...),
		lines:     NewReadFileLinesTool(workspace, restrict, maxReadFileSize, maxLines, maxLineLength, allowPaths...),
		workspace: workspace,
	}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileLinesTool) Name() string {
	return "read_file"
}

func (t *ReadFileAutoTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a file. Supports pagination via `offset` and `length`."
}

func (t *ReadFileLinesTool) Description() string {
	return "Read a UTF-8 text file from the filesystem. Output always includes line numbers in the format `LINE_NUMBER|LINE_CONTENT` (1-indexed). Supports partial reads via `start_line` and `max_lines` for large text files."
}

func (t *ReadFileAutoTool) Description() string {
	return "Read a file. Text files return line-numbered output in the format `LINE_NUMBER|LINE_CONTENT` (1-indexed) and support `start_line`/`max_lines`. Binary files fall back to raw bytes and support `offset`/`length`; passing `offset` or `length` forces raw byte mode."
}

func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Byte offset to start reading from.",
				"default":     0,
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Maximum number of bytes to read.",
				"default":     t.maxSize,
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileLinesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-indexed, inclusive).",
				"default":     1,
			},
			"max_lines": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileAutoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Text files: line number to start reading from (1-indexed, inclusive).",
				"default":     1,
			},
			"max_lines": map[string]any{
				"type":        "integer",
				"description": "Text files: maximum number of lines to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Binary files or raw reads: byte offset to start reading from. Providing offset or length forces byte mode.",
				"default":     0,
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Binary files or raw reads: maximum number of bytes to read.",
				"default":     t.bytes.maxSize,
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	// offset (optional, default 0)
	offset, err := toolshared.Int64Arg(args, "offset", 0)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if offset < 0 {
		return ErrorResult("offset must be >= 0")
	}

	// length (optional, capped at MaxReadFileSize)
	length, err := toolshared.Int64Arg(args, "length", t.maxSize)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if length <= 0 {
		return ErrorResult("length must be > 0")
	}
	if length > t.maxSize {
		length = t.maxSize
	}

	file, err := t.fs.Open(path)
	if err != nil {
		return ErrorResult(err.Error())
	}
	defer file.Close()

	// measure total size
	totalSize := int64(-1) // -1 means unknown
	if info, statErr := file.Stat(); statErr == nil {
		totalSize = info.Size()
	}

	// sniff the first 512 bytes to detect binary content before loading
	// it into the LLM context. Seeking back to 0 afterwards restores state.
	sniff := make([]byte, 512)
	sniffN, _ := file.Read(sniff)

	// Reset read position to beginning before applying the caller's offset.
	if seeker, ok := file.(io.Seeker); ok {
		_, err = seeker.Seek(0, io.SeekStart)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to reset file position after sniff: %v", err))
		}
	} else {
		// Non-seekable: we consumed sniffN bytes above; account for them when
		// discarding to reach the requested offset below.
		// If offset < sniffN the data we already read covers it, which we
		// cannot replay on a non-seekable stream — return a clear error.
		if offset < int64(sniffN) && offset > 0 {
			return ErrorResult(
				"non-seekable file: cannot seek to an offset within the first 512 bytes after binary detection",
			)
		}
	}

	// Seek to the requested offset.
	if seeker, ok := file.(io.Seeker); ok {
		_, err = seeker.Seek(offset, io.SeekStart)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to seek to offset %d: %v", offset, err))
		}
	} else if offset > 0 {
		// Fallback for non-seekable streams: discard leading bytes.
		// sniffN bytes were already consumed above, so subtract them.
		remaining := offset - int64(sniffN)
		if remaining > 0 {
			_, err = io.CopyN(io.Discard, file, remaining)
			if err != nil {
				return ErrorResult(fmt.Sprintf("failed to advance to offset %d: %v", offset, err))
			}
		}
	}

	// read length+1 bytes to reliably detect whether more content exists
	// without relying on totalSize (which may be -1 for non-seekable streams).
	// This avoids the false-positive TRUNCATED message on the last page.
	probe := make([]byte, length+1)
	n, err := io.ReadFull(file, probe)
	// FIX: io.ReadFull returns io.ErrUnexpectedEOF for partial reads (0 < n < len),
	// and io.EOF only when n == 0. Both are normal terminal conditions — only
	// other errors are genuine failures.
	if err != nil && err != io.EOF && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrorResult(fmt.Sprintf("failed to read file content: %v", err))
	}

	// hasMore is true only when we actually got the extra probe byte.
	hasMore := int64(n) > length
	data := probe[:min(int64(n), length)]

	if len(data) == 0 {
		return NewToolResult("[END OF FILE - no content at this offset]")
	}

	// Build metadata header.
	// use filepath.Base(path) instead of the raw path to avoid leaking
	// internal filesystem structure into the LLM context.
	readEnd := offset + int64(len(data))
	// use ASCII hyphen-minus instead of en-dash (U+2013) to keep the
	// header parseable by downstream tools and log processors.
	readRange := fmt.Sprintf("bytes %d-%d", offset, readEnd-1)

	displayPath := filepath.Base(path)
	var header string
	if totalSize >= 0 {
		header = fmt.Sprintf(
			"[file: %s | total: %d bytes | read: %s]",
			displayPath, totalSize, readRange,
		)
	} else {
		header = fmt.Sprintf(
			"[file: %s | read: %s | total size unknown]",
			displayPath, readRange,
		)
	}

	if hasMore {
		header += fmt.Sprintf(
			"\n[TRUNCATED - file has more content. Call read_file again with offset=%d to continue.]",
			readEnd,
		)
	} else {
		header += "\n[END OF FILE - no further content.]"
	}

	logger.DebugCF("tool", "ReadFileTool execution completed successfully",
		map[string]any{
			"path":       path,
			"bytes_read": len(data),
			"has_more":   hasMore,
		})

	// Scrub credential-looking values found in the file before the content
	// reaches the model. The header above is built from the raw byte counts,
	// so the reported range and the pagination offsets stay valid.
	return NewToolResult(header + "\n\n" + redactSecrets(string(data)))
}

func (t *ReadFileLinesTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	startLine, err := toolshared.Int64Arg(args, "start_line", 1)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if startLine < 1 {
		return ErrorResult("start_line must be >= 1")
	}
	if _, exists := args["offset"]; exists {
		return ErrorResult("offset is not supported in line mode; use start_line")
	}
	if _, exists := args["length"]; exists {
		return ErrorResult("length is not supported in line mode; use max_lines")
	}
	if _, exists := args["limit"]; exists {
		return ErrorResult("limit is not supported in line mode; use max_lines")
	}

	limit := int64(-1)
	if raw, exists := args["max_lines"]; exists && raw != nil {
		limit, err = toolshared.Int64Arg(args, "max_lines", -1)
		if err != nil {
			return ErrorResult(err.Error())
		}
		if limit <= 0 {
			return ErrorResult("max_lines, if provided, must be > 0")
		}
	}
	if limit > int64(t.maxLines) {
		limit = int64(t.maxLines)
	}

	file, err := t.fs.Open(path)
	if err != nil {
		return ErrorResult(err.Error())
	}
	defer file.Close()

	totalSize := int64(-1)
	if info, statErr := file.Stat(); statErr == nil {
		if info.IsDir() {
			return ErrorResult(fmt.Sprintf("failed to open file: path is a directory: %s", path))
		}
		totalSize = info.Size()
	}

	sample := make([]byte, 512)
	sampleN, readErr := file.Read(sample)
	if readErr != nil && readErr != io.EOF {
		return ErrorResult(fmt.Sprintf("failed to read file: %v", readErr))
	}
	sample = sample[:sampleN]
	if isBinaryReadFileData(sample) {
		return ErrorResult("file appears to be binary; switch read_file mode to 'bytes' for byte-based inspection")
	}

	reader := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(sample), file), 32*1024)

	var content strings.Builder
	if totalSize > 0 {
		content.Grow(int(min(totalSize, t.maxSize, int64(lineOutputPreallocLimit))))
	}
	lineIndex := int64(1)
	var linesRead int64
	var fileBytesRead int64
	var outputBytesRead int64
	var reachedEOF bool
	var byteBudgetTruncated bool
	var lineTruncated bool
	var linesTruncated int64
	var firstLineByteOffset int64

	for lineIndex < startLine {
		hasLine, consumedBytes, consumeErr := consumeNextLine(reader)
		if consumeErr != nil {
			return ErrorResult(fmt.Sprintf("failed to read file content: %v", consumeErr))
		}
		if !hasLine {
			reachedEOF = true
			break
		}
		firstLineByteOffset += consumedBytes
		lineIndex++
	}

	// Reuse one line buffer and one prefix buffer across iterations: the line
	// content is copied into content before the next read, so nothing aliases.
	lineBuf := make([]byte, 0, 4096)
	var prefixBuf [24]byte
	for !reachedEOF && (limit < 0 || linesRead < limit) {
		prefix := strconv.AppendInt(prefixBuf[:0], lineIndex, 10)
		prefix = append(prefix, '|')
		remaining := t.maxSize - outputBytesRead - int64(len(prefix))
		if remaining <= 0 {
			byteBudgetTruncated = true
			break
		}

		// Reading a line is capped so a single long line cannot eat the whole
		// byte budget. The +1 byte leaves room for the trailing newline, so a
		// line cut at this allowance is guaranteed to have more than
		// maxLineLength runes.
		allowance := remaining
		lineAllowance := int64(0)
		if t.maxLineLength > 0 {
			lineAllowance = int64(t.maxLineLength)*int64(utf8.UTFMax) + 1
			if lineAllowance < allowance {
				allowance = lineAllowance
			}
		}

		line, complete, hasLine, restPending, consumedBytes, readLineErr := readNextLinePrefix(
			reader,
			lineBuf,
			allowance,
		)
		if readLineErr != nil {
			return ErrorResult(fmt.Sprintf("failed to read file content: %v", readLineErr))
		}
		if !hasLine {
			reachedEOF = true
			break
		}
		lineBuf = line[:0]

		hadTrailingNewline := len(line) > 0 && line[len(line)-1] == '\n'

		cutAtLineAllowance := !complete && lineAllowance > 0 && allowance == lineAllowance
		cutAtByteBudget := !complete && !cutAtLineAllowance

		displayLine := line
		lineCutByLimit := false
		if t.maxLineLength > 0 && len(displayLine) > t.maxLineLength {
			if cut := []byte(toolshared.RunePrefix(string(displayLine), t.maxLineLength)); len(cut) < len(displayLine) {
				displayLine = cut
				lineCutByLimit = true
			}
		}
		if lineCutByLimit {
			displayLine = append(displayLine, lineTruncationMarker...)
			if complete && hadTrailingNewline {
				displayLine = append(displayLine, '\n')
			}
			linesTruncated++
		}

		if cutAtLineAllowance && (len(displayLine) == 0 || displayLine[len(displayLine)-1] != '\n') {
			// The prefix stops mid-line: terminate the display line so the
			// next line number is not glued to the truncation marker.
			displayLine = append(displayLine, '\n')
		}

		// Rendered content is scrubbed before it reaches the model; byte
		// accounting below stays on the raw line, so read budgets are
		// unaffected by redaction.
		content.Write(prefix)
		content.WriteString(redactSecrets(string(displayLine)))
		fileBytesRead += consumedBytes
		outputBytesRead += int64(len(prefix) + len(displayLine))
		linesRead++
		lineIndex++

		if cutAtLineAllowance {
			// Discard any unread remainder of the oversized line so the next
			// iteration starts at the next line.
			if restPending {
				_, discardedBytes, consumeErr := consumeNextLine(reader)
				if consumeErr != nil {
					return ErrorResult(fmt.Sprintf("failed to read file content: %v", consumeErr))
				}
				fileBytesRead += discardedBytes
			}
			continue
		}

		if cutAtByteBudget {
			byteBudgetTruncated = true
			lineTruncated = true
			break
		}
	}

	if !reachedEOF && !lineTruncated {
		hasMoreContent, peekErr := readerHasMoreContent(reader)
		if peekErr != nil {
			return ErrorResult(fmt.Sprintf("failed to inspect remaining file content: %v", peekErr))
		}
		if !hasMoreContent {
			reachedEOF = true
			byteBudgetTruncated = false
		}
	}

	if linesRead == 0 && content.Len() == 0 {
		return NewToolResult(fmt.Sprintf("[END OF FILE - no content at or after start_line=%d]", startLine))
	}

	start := startLine
	endLine := startLine + linesRead - 1
	displayPath := filepath.Base(path)
	header := fmt.Sprintf(
		"[file: %s | read: lines %d-%d (1-indexed) | file_bytes: %d | output_bytes: %d | first_line_byte_offset: %d]",
		displayPath, start, endLine, fileBytesRead, outputBytesRead, firstLineByteOffset,
	)

	switch {
	case lineTruncated:
		header += fmt.Sprintf(
			"\n[TRUNCATED - line %d exceeded the %d byte read budget and was cut mid-line.]",
			endLine,
			t.maxSize,
		)
	case byteBudgetTruncated:
		if limit > 0 {
			header += fmt.Sprintf(
				"\n[TRUNCATED - byte budget reached. Call read_file again with start_line=%d and max_lines=%d to continue at the next line.]",
				startLine+linesRead,
				limit,
			)
		} else {
			header += fmt.Sprintf(
				"\n[TRUNCATED - byte budget reached. Call read_file again with start_line=%d to continue at the next line.]",
				startLine+linesRead,
			)
		}
	case !reachedEOF && limit > 0 && linesRead >= limit:
		header += fmt.Sprintf(
			"\n[PARTIAL - more content remains. Call read_file again with start_line=%d and max_lines=%d to continue.]",
			startLine+linesRead,
			limit,
		)
	default:
		header += "\n[END OF FILE - no further content.]"
	}

	logger.DebugCF("tool", "ReadFileTool execution completed successfully",
		map[string]any{
			"path":                   path,
			"lines_read":             linesRead,
			"file_bytes_read":        fileBytesRead,
			"output_bytes_read":      outputBytesRead,
			"first_line_byte_offset": firstLineByteOffset,
			"truncated":              byteBudgetTruncated,
			"lines_truncated":        linesTruncated,
			"tool":                   t.Name(),
		})

	return NewToolResult(header + "\n\n" + content.String())
}

func (t *ReadFileAutoTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	lineArgs := hasReadFileArg(args, "start_line", "max_lines")
	byteArgs := hasReadFileArg(args, "offset", "length")
	if lineArgs && byteArgs {
		return ErrorResult(
			"use either start_line/max_lines (text files) or offset/length (binary or raw reads), not both",
		)
	}

	// An explicit byte range keeps the pre-auto behavior and lets the model
	// page through text files that are better read raw (for example minified
	// files with very long lines).
	if byteArgs {
		return t.bytes.Execute(ctx, args)
	}

	binary, errResult := t.isBinary(path)
	if errResult != nil {
		return errResult
	}
	if !binary {
		return t.lines.Execute(ctx, args)
	}
	if lineArgs {
		return ErrorResult("file appears to be binary; use offset/length to read raw bytes")
	}
	return t.bytes.Execute(ctx, args)
}

func (t *ReadFileAutoTool) isBinary(path string) (bool, *ToolResult) {
	file, err := t.bytes.fs.Open(path)
	if err != nil {
		return false, ErrorResult(err.Error())
	}
	defer file.Close()

	if info, statErr := file.Stat(); statErr == nil && info.IsDir() {
		return false, ErrorResult(fmt.Sprintf("failed to open file: path is a directory: %s", path))
	}

	sample := make([]byte, 512)
	n, readErr := file.Read(sample)
	if readErr != nil && readErr != io.EOF {
		return false, ErrorResult(fmt.Sprintf("failed to read file: %v", readErr))
	}
	return isBinaryReadFileData(sample[:n]), nil
}

func hasReadFileArg(args map[string]any, names ...string) bool {
	for _, name := range names {
		if value, ok := args[name]; ok && value != nil {
			return true
		}
	}
	return false
}

func isBinaryReadFileData(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}

	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}

	contentType := http.DetectContentType(sample)
	if strings.HasPrefix(contentType, "text/") {
		return false
	}
	if strings.HasSuffix(contentType, "/json") ||
		strings.HasSuffix(contentType, "+json") ||
		strings.HasSuffix(contentType, "/xml") ||
		strings.HasSuffix(contentType, "+xml") ||
		strings.Contains(contentType, "javascript") {
		return false
	}

	if !utf8.Valid(sample) {
		return true
	}

	controlChars := 0
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' && b != '\b' {
			controlChars++
		}
	}

	return float64(controlChars)/float64(len(sample)) > 0.1
}

func consumeNextLine(reader *bufio.Reader) (bool, int64, error) {
	sawData := false
	var consumed int64

	for {
		fragment, err := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		if len(fragment) > 0 {
			sawData = true
		}

		switch {
		case err == nil:
			return true, consumed, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return sawData, consumed, nil
		default:
			return false, consumed, err
		}
	}
}

// readNextLinePrefix reads at most maxBytes from the start of the next line.
// complete reports whether the whole line (including its terminator, if any)
// was read. restPending reports whether the reader still holds the remainder of
// an oversized line that the caller must discard before reading the next line;
// it is false when ReadSlice already consumed the terminator. consumed is the
// total number of bytes taken from the reader for this line, including bytes
// read past maxBytes.
func readNextLinePrefix(
	reader *bufio.Reader,
	dst []byte,
	maxBytes int64,
) (line []byte, complete, hasLine, restPending bool, consumed int64, err error) {
	if maxBytes <= 0 {
		return nil, false, false, false, 0, nil
	}

	dst = dst[:0]
	sawData := false
	complete = true

	for {
		fragment, readErr := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		if len(fragment) > 0 {
			sawData = true
			if remaining := maxBytes - int64(len(dst)); remaining > 0 {
				take := len(fragment)
				if int64(take) > remaining {
					take = int(remaining)
					complete = false
				}
				dst = append(dst, fragment[:take]...)
			} else {
				complete = false
			}
		}

		switch {
		case readErr == nil:
			// The terminator (and any bytes after the returned prefix) has
			// already been consumed, so the caller must not discard again.
			return dst, complete, sawData, false, consumed, nil
		case errors.Is(readErr, bufio.ErrBufferFull):
			if !complete {
				// The terminator has not been reached yet.
				return dst, false, true, true, consumed, nil
			}
			continue
		case errors.Is(readErr, io.EOF):
			if !sawData {
				return nil, true, false, false, consumed, nil
			}
			return dst, complete, true, false, consumed, nil
		default:
			return nil, false, false, false, consumed, readErr
		}
	}
}

func readerHasMoreContent(reader *bufio.Reader) (bool, error) {
	_, err := reader.Peek(1)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, io.EOF):
		return false, nil
	default:
		return false, err
	}
}

type WriteFileTool struct {
	fs       fileSystem
	altTools []string
}

// maxWriteFileCompareBytes bounds how much write_file reads from an existing
// file to detect identical or append-shaped content. Bigger files fall back to
// the overwrite guard instead of paying an extra full read.
const maxWriteFileCompareBytes = 8 << 20

// writeVerifyChunkSize bounds the read-back buffer. The verification streams
// the file through this buffer instead of reading it into a second []byte, so
// a large write costs one fixed allocation rather than twice the content size.
const writeVerifyChunkSize = 32 * 1024

// writeAction is the action a successful write_file call performed.
type writeAction string

const (
	writeActionCreated     writeAction = "created"
	writeActionOverwritten writeAction = "overwritten"
	writeActionAppended    writeAction = "appended"
	writeActionUnchanged   writeAction = "unchanged"
)

// writeResultError is the machine-readable error code in a write_file result.
type writeResultError string

const (
	writeErrorFileExists       writeResultError = "file_exists"
	writeErrorReadBackFailed   writeResultError = "read_back_failed"
	writeErrorReadBackMismatch writeResultError = "read_back_mismatch"
)

// syntaxStatus is the verdict of a completed quick syntax check.
type syntaxStatus string

const syntaxStatusOK syntaxStatus = "ok"

// writeFileConflict is the machine-readable payload returned when write_file
// refuses to replace an existing file. The model can read path and
// recommended_action without parsing the prose error, so it does not have to
// probe the filesystem to pick its next call.
type writeFileConflict struct {
	Error             writeResultError `json:"error"`
	Path              string           `json:"path"`
	ResolvedPath      string           `json:"resolved_path"`
	RecommendedAction string           `json:"recommended_action"`
	AvailableActions  []string         `json:"available_actions"`
	ExistingBytes     int64            `json:"existing_bytes"`
	NewBytes          int              `json:"new_bytes"`
	OverwriteRequired bool             `json:"overwrite_required"`
}

// writeFileResult is the machine-readable success payload. It tells the model
// what actually happened (action), where the write landed (resolved_path), how
// many bytes reached the file (bytes_written), whether a read-back matched
// (verified), and whether the quick syntax check found a problem.
type writeFileResult struct {
	Success      bool        `json:"success"`
	Action       writeAction `json:"action"`
	ResolvedPath string      `json:"resolved_path"`
	BytesWritten int         `json:"bytes_written"`
	// Verified is true when the content was confirmed byte-for-byte: by a
	// read-back after a write, or by the equality comparison for an unchanged
	// no-op.
	Verified    bool             `json:"verified"`
	Syntax      syntaxStatus     `json:"syntax,omitempty"`
	SyntaxError string           `json:"syntax_error,omitempty"`
	Error       writeResultError `json:"error,omitempty"`
	Message     string           `json:"message,omitempty"`
	Detail      string           `json:"detail,omitempty"`
}

// pathResolver is implemented by filesystem backends that know where a tool
// path lands on the host. write_file uses it to report resolved_path without
// carrying a second copy of the backend's relative-path rules.
type pathResolver interface {
	ResolvePath(path string) string
}

// resolveToolPath asks the backend where path lands, falling back to the
// process working directory for custom backends and test doubles.
func resolveToolPath(sysFs fileSystem, path string) string {
	if resolver, ok := sysFs.(pathResolver); ok {
		return resolver.ResolvePath(path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// writePolicyChecker exposes the write tool's policy checks without opening or
// modifying the target file.
type writePolicyChecker interface {
	checkWrite(path string) error
	checkRead(path string) error
}

func NewWriteFileTool(
	workspace string,
	restrict bool,
	allowPaths ...[]*regexp.Regexp,
) *WriteFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	// Default to both alternatives so standalone callers keep the full guidance;
	// the agent wiring narrows this to the tools actually registered.
	return &WriteFileTool{
		fs: &protectedWriteFs{
			fileSystem: buildBaseFs(workspace, restrict, patterns),
			workspace:  workspace,
			restrict:   restrict,
		},
		altTools: []string{"append_file", "edit_file"},
	}
}

// SetAlternativeTools limits which alternatives the copy names, so it never
// directs the model to tools that are not available.
func (t *WriteFileTool) SetAlternativeTools(names []string) {
	present := make(map[string]bool, len(names))
	for _, name := range names {
		present[name] = true
	}
	ordered := make([]string, 0, 2)
	for _, name := range []string{"append_file", "edit_file"} {
		if present[name] {
			ordered = append(ordered, name)
		}
	}
	t.altTools = ordered
}

func (t *WriteFileTool) altToolsPhrase() string {
	return strings.Join(t.altTools, " or ")
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	desc := "Write content to a file, replacing any existing content. Content is written byte-for-byte after argument decoding. Standard JSON escaping applies: \\n for newline and \\\\n for a literal backslash-n sequence. " +
		"If the file already exists, identical content is a no-op and content that only extends the existing file is accepted; any other replacement requires overwrite=true, which replaces the ENTIRE file. " +
		"Returns JSON with the resolved absolute path, bytes_written, verified (a byte-for-byte read-back), and a quick syntax check for known extensions."
	if phrase := t.altToolsPhrase(); phrase != "" {
		desc += fmt.Sprintf(
			" To add to or change part of an existing file without losing its current contents, use %s instead.",
			phrase,
		)
	}
	return desc
}

func (t *WriteFileTool) Parameters() map[string]any {
	overwriteDesc := "Set to true to replace an existing file in full, discarding its current contents. Omit it for new files, unchanged content, or content that only extends the existing file."
	if phrase := t.altToolsPhrase(); phrase != "" {
		overwriteDesc = fmt.Sprintf(
			"Set to true to replace an existing file in full, discarding its current contents. Omit it for new files, unchanged content, or content that only extends the existing file; to preserve the current contents, use %s instead.",
			phrase,
		)
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file. Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n.",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": overwriteDesc,
				"default":     false,
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	overwrite, _ := args["overwrite"].(bool)
	contentBytes := []byte(content)

	// Apply the write policy before stat/read so a refused path reports the
	// policy error instead of a misleading "file already exists" conflict.
	checker, hasPolicy := t.fs.(writePolicyChecker)
	if hasPolicy {
		if err := checker.checkWrite(path); err != nil {
			return ErrorResult(err.Error())
		}
	}

	info, statErr := t.fs.Stat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return ErrorResult(fmt.Sprintf("failed to stat file: %v", statErr))
	}
	if exists && info.IsDir() {
		return ErrorResult(fmt.Sprintf("path is a directory, expected a file: %s", path))
	}
	regular := exists && info.Mode().IsRegular()

	// Only read when the existing file can possibly be identical to, or a
	// prefix of, the incoming content. A bigger file cannot be either, and the
	// cap keeps a large existing file from doubling the write's memory cost.
	// The read policy can forbid inspecting a path that the write policy allows
	// (for example .env.example), in which case the content is not compared.
	readAllowed := !hasPolicy || checker.checkRead(path) == nil
	var existing []byte
	canCompare := readAllowed && regular && info.Size() <= int64(len(contentBytes)) &&
		info.Size() <= maxWriteFileCompareBytes
	if canCompare {
		var readErr error
		existing, readErr = t.fs.ReadFile(path)
		if readErr != nil {
			return ErrorResult(readErr.Error())
		}
	}

	if exists && canCompare {
		if bytes.Equal(existing, contentBytes) {
			// Writing identical bytes would only churn the mtime and wake file
			// watchers, so treat it as a successful no-op. The comparison above
			// already proves the file matches, so no read-back is needed.
			return t.unchangedResult(path, contentBytes)
		}
		if bytes.HasPrefix(contentBytes, existing) {
			// The caller sent the old file plus a suffix: this is an append in
			// disguise, and applying only the suffix cannot lose existing data.
			appended := len(contentBytes) - len(existing)
			if err := appendFile(t.fs, path, contentBytes[len(existing):]); err != nil {
				return ErrorResult(err.Error())
			}
			// Verify only the appended suffix: seeking past the old end keeps
			// the read-back proportional to what this call actually wrote.
			return t.successResult(writeActionAppended, path, appended, contentBytes, len(existing))
		}
	}

	if exists && !overwrite {
		return t.conflictResult(path, info.Size(), len(contentBytes))
	}

	if err := t.fs.WriteFile(path, contentBytes); err != nil {
		return ErrorResult(err.Error())
	}

	action := writeActionCreated
	if exists {
		action = writeActionOverwritten
	}
	return t.successResult(action, path, len(contentBytes), contentBytes, 0)
}

// successResult verifies the write and returns the structured payload. content
// is the full post-write file; verifyFrom is the offset where this call started
// writing, so an append only re-reads its suffix.
func (t *WriteFileTool) successResult(
	action writeAction, path string,
	bytesWritten int,
	content []byte,
	verifyFrom int,
) *ToolResult {
	resolved := resolveToolPath(t.fs, path)
	verified, err := verifyWrittenFile(t.fs, path, content[verifyFrom:], int64(verifyFrom))
	if err != nil {
		return writeFileFailure(
			action,
			resolved,
			bytesWritten,
			writeErrorReadBackFailed,
			"the file was written but the read-back failed; inspect it before retrying",
			err.Error(),
		)
	}
	if !verified {
		return writeFileFailure(
			action,
			resolved,
			bytesWritten,
			writeErrorReadBackMismatch,
			"the file was written but the read-back did not match; inspect it before retrying",
			"",
		)
	}

	result := writeFileResult{
		Success:      true,
		Action:       action,
		ResolvedPath: resolved,
		BytesWritten: bytesWritten,
		Verified:     true,
	}
	applySyntaxCheck(&result, path, content)
	return jsonToolResult(result, false)
}

// unchangedResult reports the no-op path. The byte comparison that detected it
// already proves the file matches, so no extra read-back is needed.
func (t *WriteFileTool) unchangedResult(path string, content []byte) *ToolResult {
	result := writeFileResult{
		Success:      true,
		Action:       writeActionUnchanged,
		ResolvedPath: resolveToolPath(t.fs, path),
		BytesWritten: 0,
		Verified:     true,
	}
	applySyntaxCheck(&result, path, content)
	return jsonToolResult(result, false)
}

// applySyntaxCheck fills the advisory syntax fields for known extensions. A
// skipped check is reported as absent rather than as a pass, so the model never
// sees "ok" for a check that did not run.
func applySyntaxCheck(result *writeFileResult, path string, content []byte) {
	check := quickSyntaxCheck(path, content)
	if !check.Checked || check.Skipped {
		return
	}
	if check.Problem == "" {
		result.Syntax = syntaxStatusOK
	} else {
		result.SyntaxError = check.Problem
	}
}

func writeFileFailure(
	action writeAction,
	resolved string,
	bytesWritten int,
	code writeResultError,
	message, detail string,
) *ToolResult {
	result := writeFileResult{
		Success:      false,
		Action:       action,
		ResolvedPath: resolved,
		BytesWritten: bytesWritten,
		Verified:     false,
		Error:        code,
		Message:      message,
		Detail:       detail,
	}
	return jsonToolResult(result, true)
}

// jsonToolResult encodes a structured tool payload. The payload types contain
// only strings, bools, and ints, so encoding cannot fail today; the error path
// keeps the tool contract explicit if a future field changes that.
func jsonToolResult(payload any, isError bool) *ToolResult {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to encode tool result: %v", err))
	}
	if isError {
		return ErrorResult(string(encoded))
	}
	return SilentResult(string(encoded))
}

// verifyWrittenFile streams the file back from offset and compares it with
// expected. The fixed buffer keeps the check's memory flat regardless of file
// size. Appends need a seekable file; every production backend opens a regular
// file through *os.File, so a non-seeker is a backend contract violation.
func verifyWrittenFile(sysFs fileSystem, path string, expected []byte, offset int64) (bool, error) {
	f, err := sysFs.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	if offset > 0 {
		seeker, ok := f.(io.Seeker)
		if !ok {
			return false, fmt.Errorf("read-back of appended content requires a seekable file")
		}
		if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
			return false, err
		}
	}

	bufSize := len(expected)
	if bufSize == 0 {
		bufSize = 1 // one spare byte detects content past an empty expected value
	}
	if bufSize > writeVerifyChunkSize {
		bufSize = writeVerifyChunkSize
	}
	buf := make([]byte, bufSize)
	matched := 0
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if matched+n > len(expected) || !bytes.Equal(buf[:n], expected[matched:matched+n]) {
				return false, nil
			}
			matched += n
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return false, readErr
		}
	}
	return matched == len(expected), nil
}

// conflictResult builds the "file already exists" error. The first line stays
// human-readable and backward compatible; the second is a structured payload
// that names the path and the safest next action.
func (t *WriteFileTool) conflictResult(path string, existingBytes int64, newBytes int) *ToolResult {
	available := make([]string, 0, len(t.altTools)+1)
	recommended := "overwrite"
	if len(t.altTools) > 0 {
		recommended = t.altTools[0]
		for _, name := range t.altTools {
			if name == "edit_file" {
				recommended = name
				break
			}
		}
		available = append(available, t.altTools...)
	}
	available = append(available, "overwrite")

	message := fmt.Sprintf(
		"file: %s already exists. Set overwrite=true only if you intend to replace the entire file, which discards its current contents.",
		path,
	)
	if phrase := t.altToolsPhrase(); phrase != "" {
		message = fmt.Sprintf(
			"file: %s already exists. To add to it or change part of it without losing the current contents, use %s. Only set overwrite=true if you intend to replace the entire file.",
			path,
			phrase,
		)
	}

	payload, err := json.Marshal(writeFileConflict{
		Error:             writeErrorFileExists,
		Path:              path,
		ResolvedPath:      resolveToolPath(t.fs, path),
		RecommendedAction: recommended,
		AvailableActions:  available,
		ExistingBytes:     existingBytes,
		NewBytes:          newBytes,
		OverwriteRequired: true,
	})
	if err != nil {
		return ErrorResult(message)
	}
	return ErrorResult(fmt.Sprintf("%s\nstructured: %s", message, payload))
}

type ListDirTool struct {
	fs        fileSystem
	workspace string
}

func NewListDirTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ListDirTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &ListDirTool{fs: buildFs(workspace, restrict, patterns), workspace: workspace}
}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path"
}

func (t *ListDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to list",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := t.fs.ReadDir(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}
	return formatDirEntries(entries)
}

func formatDirEntries(entries []os.DirEntry) *ToolResult {
	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString("DIR:  " + entry.Name() + "\n")
		} else {
			result.WriteString("FILE: " + entry.Name() + "\n")
		}
	}
	return NewToolResult(result.String())
}

// fileSystem abstracts reading, writing, and listing files, allowing both
// unrestricted (host filesystem) and sandbox (os.Root) implementations to share the same polymorphic interface.
type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
	Open(path string) (fs.File, error)
}

// hostFs is an unrestricted fileReadWriter that operates directly on the host filesystem.
type hostFs struct{}

func (h *hostFs) ReadFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to read file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func (h *hostFs) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func (h *hostFs) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

func (h *hostFs) WriteFile(path string, data []byte) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

// ResolvePath reports the absolute path hostFs targets.
func (h *hostFs) ResolvePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// fileAppender is implemented by filesystems that can append without reading
// and rewriting the whole file. Callers fall back to ReadFile+WriteFile when a
// filesystem does not implement it.
type fileAppender interface {
	AppendFile(path string, data []byte) error
}

func (h *hostFs) AppendFile(path string, data []byte) error {
	// Match WriteFile: a new nested path gets its parent directories created.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	// A file created here only survives a power loss once its directory entry
	// is synced, exactly like the rename in WriteFileAtomic.
	_, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("failed to stat file: %w", statErr)
	}
	created := errors.Is(statErr, fs.ErrNotExist)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open file for append: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to append to file: %w", err)
	}
	// Explicit sync keeps the flash-storage durability guarantee of WriteFile.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to sync file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}
	if created {
		if dirFile, err := os.Open(dir); err == nil {
			_ = dirFile.Sync()
			_ = dirFile.Close()
		}
	}
	return nil
}

func (h *hostFs) Open(path string) (fs.File, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to open file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to open file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

// sandboxFs is a sandboxed fileSystem that operates within a strictly defined workspace using os.Root.
type sandboxFs struct {
	workspace string
}

func (r *sandboxFs) execute(path string, fn func(root *os.Root, relPath string) error) error {
	if r.workspace == "" {
		return fmt.Errorf("workspace is not defined")
	}

	root, err := os.OpenRoot(r.workspace)
	if err != nil {
		return fmt.Errorf("failed to open workspace: %w", err)
	}
	defer root.Close()

	relPath, err := getSafeRelPath(r.workspace, path)
	if err != nil {
		return err
	}

	// os.Root API on Windows only accepts forward slashes (/).
	relPath = normalizeRootRelPath(relPath)

	return fn(root, relPath)
}

func (r *sandboxFs) ReadFile(path string) ([]byte, error) {
	var content []byte
	err := r.execute(path, func(root *os.Root, relPath string) error {
		fileContent, err := root.ReadFile(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to read file: file not found: %w", err)
			}
			// os.Root returns "escapes from parent" for paths outside the root
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to read file: access denied: %w", err)
			}
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = fileContent
		return nil
	})
	return content, err
}

func (r *sandboxFs) WriteFile(path string, data []byte) error {
	return r.execute(path, func(root *os.Root, relPath string) error {
		dir := filepath.Dir(relPath)
		if dir != "." && dir != "/" {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create parent directories: %w", err)
			}
		}

		// Use atomic write pattern with explicit sync for flash storage reliability.
		// Using 0o600 (owner read/write only) for secure default permissions.
		tmpRelPath := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())

		tmpFile, err := root.OpenFile(tmpRelPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to open temp file: %w", err)
		}

		if _, err := tmpFile.Write(data); err != nil {
			_ = tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}

		// CRITICAL: Force sync to storage medium before rename.
		// This ensures data is physically written to disk, not just cached.
		if err := tmpFile.Sync(); err != nil {
			_ = tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to sync temp file: %w", err)
		}

		if err := tmpFile.Close(); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to close temp file: %w", err)
		}

		if err := root.Rename(tmpRelPath, relPath); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to rename temp file over target: %w", err)
		}

		// Sync directory to ensure rename is durable
		if dirFile, err := root.Open("."); err == nil {
			_ = dirFile.Sync()
			_ = dirFile.Close()
		}

		return nil
	})
}

func (r *sandboxFs) AppendFile(path string, data []byte) error {
	return r.execute(path, func(root *os.Root, relPath string) error {
		dir := filepath.Dir(relPath)
		openDir := dir
		if openDir == "." || openDir == "/" || openDir == "" {
			openDir = "."
		} else if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directories: %w", err)
		}

		// A file created here only survives a power loss once its directory
		// entry is synced, exactly like the rename in WriteFile.
		_, statErr := root.Stat(relPath)
		if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("failed to stat file: %w", statErr)
		}
		created := errors.Is(statErr, fs.ErrNotExist)

		f, err := root.OpenFile(relPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("failed to open file for append: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			return fmt.Errorf("failed to append to file: %w", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return fmt.Errorf("failed to sync file: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("failed to close file: %w", err)
		}
		if created {
			if dirFile, err := root.Open(openDir); err == nil {
				_ = dirFile.Sync()
				_ = dirFile.Close()
			}
		}
		return nil
	})
}

func (r *sandboxFs) ReadDir(path string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	err := r.execute(path, func(root *os.Root, relPath string) error {
		dirEntries, err := fs.ReadDir(root.FS(), relPath)
		if err != nil {
			return err
		}
		entries = dirEntries
		return nil
	})
	return entries, err
}

func (r *sandboxFs) Stat(path string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := r.execute(path, func(root *os.Root, relPath string) error {
		var statErr error
		info, statErr = root.Stat(relPath)
		return statErr
	})
	return info, err
}

func (r *sandboxFs) Open(path string) (fs.File, error) {
	var f fs.File
	err := r.execute(path, func(root *os.Root, relPath string) error {
		file, err := root.Open(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to open file: file not found: %w", err)
			}
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to open file: access denied: %w", err)
			}
			return fmt.Errorf("failed to open file: %w", err)
		}
		f = file
		return nil
	})
	return f, err
}

// ResolvePath reports the workspace path sandboxFs targets.
func (r *sandboxFs) ResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if r.workspace == "" {
		return path
	}
	abs, err := filepath.Abs(r.workspace)
	if err != nil {
		return path
	}
	return filepath.Join(abs, path)
}

// whitelistFs wraps a sandboxFs and allows access to specific paths outside
// the workspace when they match any of the provided patterns.
type whitelistFs struct {
	sandbox  *sandboxFs
	host     hostFs
	patterns []*regexp.Regexp
}

func (w *whitelistFs) matches(path string) bool {
	return isAllowedPath(path, w.patterns)
}

func (w *whitelistFs) ReadFile(path string) ([]byte, error) {
	if w.matches(path) {
		return w.host.ReadFile(path)
	}
	return w.sandbox.ReadFile(path)
}

func (w *whitelistFs) WriteFile(path string, data []byte) error {
	if w.matches(path) {
		return w.host.WriteFile(path, data)
	}
	return w.sandbox.WriteFile(path, data)
}

func (w *whitelistFs) AppendFile(path string, data []byte) error {
	if w.matches(path) {
		return w.host.AppendFile(path, data)
	}
	return w.sandbox.AppendFile(path, data)
}

func (w *whitelistFs) ReadDir(path string) ([]os.DirEntry, error) {
	if w.matches(path) {
		return w.host.ReadDir(path)
	}
	return w.sandbox.ReadDir(path)
}

func (w *whitelistFs) Stat(path string) (fs.FileInfo, error) {
	if w.matches(path) {
		return w.host.Stat(path)
	}
	return w.sandbox.Stat(path)
}

func (w *whitelistFs) Open(path string) (fs.File, error) {
	if w.matches(path) {
		return w.host.Open(path)
	}
	return w.sandbox.Open(path)
}

// ResolvePath delegates to the backend that serves the path.
func (w *whitelistFs) ResolvePath(path string) string {
	if w.matches(path) {
		return w.host.ResolvePath(path)
	}
	return w.sandbox.ResolvePath(path)
}

// buildFs applies the read policy to every filesystem tool that can inspect files.
func buildFs(workspace string, restrict bool, patterns []*regexp.Regexp) fileSystem {
	return &protectedReadFs{
		fileSystem: buildBaseFs(workspace, restrict, patterns),
		workspace:  workspace,
		restrict:   restrict,
	}
}

// buildBaseFs enforces the workspace sandbox; read and write policies wrap it
// at the individual tool boundaries.
func buildBaseFs(workspace string, restrict bool, patterns []*regexp.Regexp) fileSystem {
	if !restrict {
		return &hostFs{}
	}
	sandbox := &sandboxFs{workspace: workspace}
	if len(patterns) > 0 {
		return &whitelistFs{sandbox: sandbox, patterns: patterns}
	}
	return sandbox
}

func normalizeRootRelPath(relPath string) string {
	return normalizeRootRelPathForSeparator(relPath, os.PathSeparator)
}

func normalizeRootRelPathForSeparator(relPath string, sep rune) string {
	if sep == '\\' {
		return strings.ReplaceAll(relPath, `\`, `/`)
	}
	return relPath
}

// Helper to get a safe relative path for os.Root usage
func getSafeRelPath(workspace, path string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace is not defined")
	}

	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		var err error
		rel, err = filepath.Rel(workspace, rel)
		if err != nil {
			return "", fmt.Errorf("failed to calculate relative path: %w", err)
		}
	}

	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}

	return rel, nil
}
