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

// v4aHunk is one update hunk. Before holds context and removed lines, After
// holds context and added lines, both in file order.
type v4aHunk struct {
	before    []string
	after     []string
	endOfFile bool
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
			current.after = append(current.after, line[1:])
		case strings.HasPrefix(line, "-"):
			if current == nil {
				current = &v4aHunk{}
			}
			current.before = append(current.before, line[1:])
		case strings.HasPrefix(line, " "):
			if current == nil {
				current = &v4aHunk{}
			}
			current.before = append(current.before, line[1:])
			current.after = append(current.after, line[1:])
		case line == "":
			// Inside a hunk a blank line is an empty context line.
			if current != nil {
				current.before = append(current.before, "")
				current.after = append(current.after, "")
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
	if hunk == nil || len(hunk.before) == 0 && len(hunk.after) == 0 {
		return
	}
	if len(hunk.before) == len(hunk.after) && slices.Equal(hunk.before, hunk.after) {
		return
	}
	parsed.hunks = append(parsed.hunks, *hunk)
}

// applyV4AHunks locates every hunk in content and applies the replacements.
// Matching starts at the end of the previous hunk and falls back to the whole
// file so out-of-order hunks still apply. Either search must produce a unique
// match, so a hunk with too little context fails instead of editing the wrong
// occurrence.
func applyV4AHunks(content []byte, hunks []v4aHunk) ([]byte, error) {
	text := string(content)
	crlf := usesOnlyCRLF(text)
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}

	lines := strings.Split(text, "\n")
	cursor := 0
	for index, hunk := range hunks {
		start, err := findHunkMatch(lines, hunk.before, cursor)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", index+1, err)
		}

		end := start + len(hunk.before)
		if hunk.endOfFile && !isFileEnd(lines, end) {
			return nil, fmt.Errorf("hunk %d: %q does not match the end of the file", index+1, v4aEndOfFile)
		}

		lines = replaceLines(lines, start, end, hunk.after)
		cursor = start + len(hunk.after)
	}

	result := strings.Join(lines, "\n")
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return []byte(result), nil
}

// errHunkNotFound reports that no position matched a hunk's context.
var errHunkNotFound = errors.New("hunk context not found")

func findHunkMatch(lines, before []string, cursor int) (int, error) {
	if len(before) == 0 {
		return cursor, nil
	}

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

func findUniqueHunkMatch(lines, before []string, start int) (int, error) {
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

func findLineSequence(lines, sequence []string, start int, tolerant bool) []int {
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

func linesMatchAt(lines, sequence []string, start int, tolerant bool) bool {
	for offset, want := range sequence {
		if !patchLineMatches(lines[start+offset], want, tolerant) {
			return false
		}
	}
	return true
}

func patchLineMatches(have, want string, tolerant bool) bool {
	have = strings.TrimSuffix(have, "\r")
	if tolerant {
		return strings.TrimRight(have, " \t") == strings.TrimRight(want, " \t")
	}
	return have == want
}

func replaceLines(lines []string, start, end int, replacement []string) []string {
	result := make([]string, 0, len(lines)-(end-start)+len(replacement))
	result = append(result, lines[:start]...)
	result = append(result, replacement...)
	result = append(result, lines[end:]...)
	return result
}

// isFileEnd reports whether position sits at the end of the file, tolerating
// the empty trailing element produced by a final newline.
func isFileEnd(lines []string, position int) bool {
	if position == len(lines) {
		return true
	}
	return position == len(lines)-1 && lines[position] == ""
}

func usesOnlyCRLF(content string) bool {
	newlines := strings.Count(content, "\n")
	return newlines > 0 && strings.Count(content, "\r\n") == newlines
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
