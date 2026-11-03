package fstools

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const (
	v4aBeginPatch = "*** Begin Patch"
	v4aEndPatch   = "*** End Patch"
	v4aUpdateFile = "*** Update File:"
	v4aAddFile    = "*** Add File:"
	v4aDeleteFile = "*** Delete File:"
	v4aMoveTo     = "*** Move to:"
	v4aEndOfFile  = "*** End of File"
)

// v4aPatch is a parsed V4A patch section for a single file.
type v4aPatch struct {
	path  string
	hunks []v4aHunk
}

// v4aOpKind identifies one line in a hunk.
type v4aOpKind byte

const (
	v4aContextOp v4aOpKind = ' '
	v4aRemoveOp  v4aOpKind = '-'
	v4aAddOp     v4aOpKind = '+'
)

// v4aOp is one context, removed, or added line. Keeping the operation kind
// lets the applier reuse the real file line for context entries, including its
// trailing whitespace, instead of substituting the patch text.
type v4aOp struct {
	kind v4aOpKind
	text string
}

// v4aHunk is one update hunk in patch order.
type v4aHunk struct {
	ops       []v4aOp
	endOfFile bool
}

// beforeLines returns the context and removed lines used for matching.
func (h v4aHunk) beforeLines() []string {
	lines := make([]string, 0, len(h.ops))
	for _, op := range h.ops {
		if op.kind != v4aAddOp {
			lines = append(lines, op.text)
		}
	}
	return lines
}

// afterLines returns the context and added lines used for a context-free
// append at end of file.
func (h v4aHunk) afterLines() []string {
	lines := make([]string, 0, len(h.ops))
	for _, op := range h.ops {
		if op.kind != v4aRemoveOp {
			lines = append(lines, op.text)
		}
	}
	return lines
}

// applyV4APatch applies a V4A-formatted patch to content. The patch may include
// the "*** Begin Patch" / "*** End Patch" wrapper and a "*** Update File:"
// header; when the header is present it must target path.
func applyV4APatch(content []byte, patch, path string) ([]byte, error) {
	parsed, err := parseV4APatch(patch)
	if err != nil {
		return nil, err
	}
	if parsed.path != "" && !patchHeaderMatchesPath(parsed.path, path) {
		return nil, fmt.Errorf("patch targets %q, but path is %q", parsed.path, path)
	}
	return applyV4AHunks(content, parsed.hunks)
}

// parseV4APatch parses the V4A subset supported by edit_file: a single Update
// File section with one or more hunks. The Begin/End wrapper and the Update
// File header are optional so callers can pass the hunk body alone.
func parseV4APatch(patch string) (v4aPatch, error) {
	var parsed v4aPatch
	var current *v4aHunk
	var seenBegin, seenEnd, seenEndOfFile bool

	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	// A trailing newline ends the last hunk line; it is not an empty context
	// line. Wrapped patches ignore the blank line after "*** End Patch".
	patch = strings.TrimSuffix(patch, "\n")
	lines := strings.Split(patch, "\n")
	for index, raw := range lines {
		lineNumber := index + 1
		line := strings.TrimSuffix(raw, "\r")

		switch {
		case line == v4aBeginPatch:
			if seenBegin {
				return v4aPatch{}, fmt.Errorf("line %d: duplicate %q", lineNumber, v4aBeginPatch)
			}
			if seenEnd || parsed.path != "" || len(parsed.hunks) > 0 || current != nil {
				return v4aPatch{}, fmt.Errorf("line %d: %q must be the first patch line", lineNumber, v4aBeginPatch)
			}
			seenBegin = true
		case line == v4aEndPatch:
			if !seenBegin {
				return v4aPatch{}, fmt.Errorf("line %d: %q requires %q", lineNumber, v4aEndPatch, v4aBeginPatch)
			}
			if seenEnd {
				return v4aPatch{}, fmt.Errorf("line %d: duplicate %q", lineNumber, v4aEndPatch)
			}
			finishHunk(&parsed, &current)
			seenEnd = true
		case seenEndOfFile && strings.TrimSpace(line) != "":
			return v4aPatch{}, fmt.Errorf("line %d: unexpected content after %q", lineNumber, v4aEndOfFile)
		case seenEnd:
			if strings.TrimSpace(line) != "" {
				return v4aPatch{}, fmt.Errorf("line %d: unexpected content after %q", lineNumber, v4aEndPatch)
			}
		case strings.HasPrefix(line, v4aUpdateFile):
			if parsed.path != "" {
				return v4aPatch{}, fmt.Errorf(
					"line %d: edit_file applies one file per call; split the patch into separate calls",
					lineNumber,
				)
			}
			parsed.path = strings.TrimSpace(strings.TrimPrefix(line, v4aUpdateFile))
			if parsed.path == "" {
				return v4aPatch{}, fmt.Errorf("line %d: %q requires a path", lineNumber, v4aUpdateFile)
			}
		case strings.HasPrefix(line, v4aAddFile), strings.HasPrefix(line, v4aDeleteFile):
			return v4aPatch{}, fmt.Errorf(
				"line %d: %q is not supported by edit_file; use the dedicated file tool",
				lineNumber,
				strings.TrimSpace(line),
			)
		case strings.HasPrefix(line, v4aMoveTo):
			return v4aPatch{}, fmt.Errorf("line %d: %q is not supported by edit_file", lineNumber, v4aMoveTo)
		case line == v4aEndOfFile:
			if current == nil {
				return v4aPatch{}, fmt.Errorf("line %d: %q must follow a hunk", lineNumber, v4aEndOfFile)
			}
			current.endOfFile = true
			seenEndOfFile = true
		case strings.HasPrefix(line, "@@"):
			finishHunk(&parsed, &current)
			current = &v4aHunk{}
		case strings.HasPrefix(line, "+"):
			if current == nil {
				current = &v4aHunk{}
			}
			current.ops = append(current.ops, v4aOp{kind: v4aAddOp, text: line[1:]})
		case strings.HasPrefix(line, "-"):
			if current == nil {
				current = &v4aHunk{}
			}
			current.ops = append(current.ops, v4aOp{kind: v4aRemoveOp, text: line[1:]})
		case strings.HasPrefix(line, " "):
			if current == nil {
				current = &v4aHunk{}
			}
			current.ops = append(current.ops, v4aOp{kind: v4aContextOp, text: line[1:]})
		case line == "":
			// Inside a hunk a blank line is an empty context line.
			if current != nil {
				current.ops = append(current.ops, v4aOp{kind: v4aContextOp})
			}
		case strings.HasPrefix(line, "*** "):
			return v4aPatch{}, fmt.Errorf("line %d: unsupported patch directive %q", lineNumber, line)
		default:
			return v4aPatch{}, fmt.Errorf(
				"line %d: invalid patch line %q (lines must start with ' ', '+', '-', '@@', or '***')",
				lineNumber,
				line,
			)
		}
	}

	if seenBegin && !seenEnd {
		return v4aPatch{}, fmt.Errorf("missing %q", v4aEndPatch)
	}
	finishHunk(&parsed, &current)
	if len(parsed.hunks) == 0 {
		return v4aPatch{}, fmt.Errorf("patch contains no hunks")
	}
	return parsed, nil
}

// finishHunk appends the current hunk when it changes something. Empty and
// no-op hunks are ignored so a stray "@@" line does not fail a valid patch.
func finishHunk(parsed *v4aPatch, current **v4aHunk) {
	hunk := *current
	*current = nil
	if hunk == nil {
		return
	}
	before := hunk.beforeLines()
	after := hunk.afterLines()
	if len(before) == 0 && len(after) == 0 {
		return
	}
	if len(before) == len(after) && slices.Equal(before, after) {
		return
	}
	parsed.hunks = append(parsed.hunks, *hunk)
}

// fileLine is one logical line with its original terminator. Keeping the
// terminator separate lets matching ignore CRLF while the writer preserves a
// file that mixes LF and CRLF.
type fileLine struct {
	text string
	eol  string // "\n", "\r\n", or "" for a final line without a newline
}

func splitFileLines(content string) []fileLine {
	var lines []fileLine
	for len(content) > 0 {
		index := strings.IndexByte(content, '\n')
		if index < 0 {
			lines = append(lines, fileLine{text: content})
			break
		}
		text := content[:index]
		eol := "\n"
		if strings.HasSuffix(text, "\r") {
			text = strings.TrimSuffix(text, "\r")
			eol = "\r\n"
		}
		lines = append(lines, fileLine{text: text, eol: eol})
		content = content[index+1:]
	}
	return lines
}

func renderFileLines(lines []fileLine) []byte {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.text)
		builder.WriteString(line.eol)
	}
	return []byte(builder.String())
}

// defaultEOL returns the first line ending used by the file, or LF when the
// file has no line ending to copy.
func defaultEOL(lines []fileLine) string {
	for _, line := range lines {
		if line.eol != "" {
			return line.eol
		}
	}
	return "\n"
}

// normalizeFinalEOL keeps the original trailing-newline state while making
// sure every non-final line has a separator.
func normalizeFinalEOL(lines []fileLine, trailingNewline bool) {
	if len(lines) == 0 {
		return
	}
	fallback := defaultEOL(lines)
	for index := 0; index < len(lines)-1; index++ {
		if lines[index].eol == "" {
			lines[index].eol = fallback
		}
	}
	last := &lines[len(lines)-1]
	if trailingNewline {
		if last.eol == "" {
			last.eol = fallback
		}
		return
	}
	last.eol = ""
}

// applyV4AHunks locates every hunk in content and applies the replacements.
// Matching starts at the end of the previous hunk and falls back to the whole
// file so out-of-order hunks still apply. Either search must produce a unique
// match, so a hunk with too little context fails instead of editing the wrong
// occurrence.
func applyV4AHunks(content []byte, hunks []v4aHunk) ([]byte, error) {
	text := string(content)
	lines := splitFileLines(text)
	// Preserve the historical empty-file append result ("tail\n") while still
	// honoring an explicit missing trailing newline for non-empty files.
	trailingNewline := strings.HasSuffix(text, "\n") || len(lines) == 0

	cursor := 0
	for index, hunk := range hunks {
		before := hunk.beforeLines()
		if len(before) == 0 {
			if !hunk.endOfFile {
				return nil, fmt.Errorf(
					"hunk %d: hunk has no context; add a context or removed line so the insertion point is unambiguous",
					index+1,
				)
			}
			position := endOfFileInsertPosition(lines)
			after := hunk.afterLines()
			added := make([]fileLine, 0, len(after))
			eol := eolForAppend(lines)
			if len(lines) > 0 && lines[len(lines)-1].eol == "" {
				lines[len(lines)-1].eol = eol
			}
			for _, line := range after {
				added = append(added, fileLine{text: line, eol: eol})
			}
			lines = replaceLines(lines, position, position, added)
			cursor = position + len(added)
			continue
		}

		start, err := findHunkMatch(lines, before, cursor)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", index+1, err)
		}

		end := start + len(before)
		if hunk.endOfFile && !isFileEnd(lines, end) {
			return nil, fmt.Errorf("hunk %d: %q does not match the end of the file", index+1, v4aEndOfFile)
		}

		replacement := hunkReplacement(hunk, lines, start)
		lines = replaceLines(lines, start, end, replacement)
		cursor = start + len(replacement)
	}

	normalizeFinalEOL(lines, trailingNewline)
	return renderFileLines(lines), nil
}

// eolForAppend picks the line ending for lines appended at end of file.
func eolForAppend(lines []fileLine) string {
	if len(lines) > 0 && lines[len(lines)-1].eol != "" {
		return lines[len(lines)-1].eol
	}
	return defaultEOL(lines)
}

// hunkReplacement builds the output lines for a matched hunk. Context lines
// come from the file itself, so a tolerant match never rewrites trailing
// whitespace the patch did not explicitly remove or add. Added lines copy the
// line ending of the replaced line, then the preceding context, then the next
// line, so a mixed-ending file keeps its local style.
func hunkReplacement(hunk v4aHunk, lines []fileLine, start int) []fileLine {
	replacement := make([]fileLine, 0, len(hunk.ops))
	fileIndex := start
	lastRemovedEOL := ""
	lastEmittedEOL := ""
	fallback := defaultEOL(lines)
	for _, op := range hunk.ops {
		switch op.kind {
		case v4aContextOp:
			line := lines[fileIndex]
			fileIndex++
			replacement = append(replacement, line)
			lastEmittedEOL = line.eol
			lastRemovedEOL = ""
		case v4aRemoveOp:
			lastRemovedEOL = lines[fileIndex].eol
			fileIndex++
		case v4aAddOp:
			eol := lastRemovedEOL
			if eol == "" {
				eol = lastEmittedEOL
			}
			if eol == "" && fileIndex < len(lines) {
				eol = lines[fileIndex].eol
			}
			if eol == "" {
				eol = fallback
			}
			replacement = append(replacement, fileLine{text: op.text, eol: eol})
			lastEmittedEOL = eol
		}
	}
	return replacement
}

// errHunkNotFound reports that no position matched a hunk's context.
var errHunkNotFound = errors.New("hunk context not found")

func findHunkMatch(lines []fileLine, before []string, cursor int) (int, error) {
	start, err := findUniqueHunkMatch(lines, before, cursor)
	if err == nil {
		return start, nil
	}
	if !errors.Is(err, errHunkNotFound) {
		return 0, err
	}

	// Hunks normally describe the file from top to bottom, but models sometimes
	// emit them out of order. Retry on the whole file and still require a single
	// match, so an ambiguous context keeps failing instead of editing the wrong
	// occurrence.
	if cursor > 0 {
		start, err = findUniqueHunkMatch(lines, before, 0)
		if err == nil {
			return start, nil
		}
		if !errors.Is(err, errHunkNotFound) {
			return 0, err
		}
	}

	return 0, fmt.Errorf("context not found; check that the context lines match the current file")
}

func findUniqueHunkMatch(lines []fileLine, before []string, start int) (int, error) {
	for _, tolerant := range []bool{false, true} {
		matches := findLineSequence(lines, before, start, tolerant)
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			continue
		default:
			return 0, fmt.Errorf(
				"context matches %d times; add more context lines to make it unique",
				len(matches),
			)
		}
	}

	return 0, errHunkNotFound
}

func findLineSequence(lines []fileLine, sequence []string, start int, tolerant bool) []int {
	if len(sequence) == 0 || start < 0 || start+len(sequence) > len(lines) {
		return nil
	}

	var matches []int
	for index := start; index+len(sequence) <= len(lines); index++ {
		if linesMatchAt(lines, sequence, index, tolerant) {
			matches = append(matches, index)
		}
	}
	return matches
}

func linesMatchAt(lines []fileLine, sequence []string, start int, tolerant bool) bool {
	for offset, want := range sequence {
		if !patchLineMatches(lines[start+offset], want, tolerant) {
			return false
		}
	}
	return true
}

func patchLineMatches(have fileLine, want string, tolerant bool) bool {
	haveText := have.text
	if have.eol == "" {
		// Preserve the old behavior for a final line followed by a bare CR.
		haveText = strings.TrimSuffix(haveText, "\r")
	}
	if tolerant {
		return strings.TrimRight(haveText, " \t") == strings.TrimRight(want, " \t")
	}
	return haveText == want
}

func replaceLines(lines []fileLine, start, end int, replacement []fileLine) []fileLine {
	result := make([]fileLine, 0, len(lines)-(end-start)+len(replacement))
	result = append(result, lines[:start]...)
	result = append(result, replacement...)
	result = append(result, lines[end:]...)
	return result
}

// endOfFileInsertPosition is the position after the last logical line.
func endOfFileInsertPosition(lines []fileLine) int {
	return len(lines)
}

// isFileEnd reports whether position sits at the end of the file.
func isFileEnd(lines []fileLine, position int) bool {
	return position == len(lines)
}

func patchHeaderMatchesPath(header, path string) bool {
	header = filepath.Clean(filepath.FromSlash(strings.TrimSpace(header)))
	path = filepath.Clean(path)
	if header == path {
		return true
	}
	return !filepath.IsAbs(header) &&
		filepath.IsAbs(path) &&
		strings.HasSuffix(filepath.ToSlash(path), "/"+filepath.ToSlash(header))
}
