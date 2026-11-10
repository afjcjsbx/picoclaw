package toolshared

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiffResult_UnifiedDiffForModelAndUser(t *testing.T) {
	result := DiffResult("/tmp/example.txt", []byte("alpha\nbeta\ngamma\n"), []byte("alpha\nbeta 2\ngamma\n"))

	if result == nil {
		t.Fatal("DiffResult() returned nil")
	}
	if result.Silent {
		t.Fatal("expected DiffResult to be user-visible")
	}
	if result.IsError {
		t.Fatal("expected DiffResult to be successful")
	}

	// A small diff fits both budgets, so the model and the UI share the
	// exact same string (and backing memory).
	if result.ForLLM != result.ForUser {
		t.Fatalf("expected shared model/user diff, got\nForLLM: %q\nForUser: %q",
			result.ForLLM, result.ForUser)
	}

	for _, want := range []string{
		"File edited: /tmp/example.txt",
		"```diff",
		"--- a/tmp/example.txt",
		"+++ b/tmp/example.txt",
		"@@ -1,3 +1,3 @@",
		" alpha",
		"-beta",
		"+beta 2",
		" gamma",
	} {
		if !strings.Contains(result.ForLLM, want) {
			t.Fatalf("model-facing diff missing %q:\n%s", want, result.ForLLM)
		}
		if !strings.Contains(result.ForUser, want) {
			t.Fatalf("user-facing diff missing %q:\n%s", want, result.ForUser)
		}
	}
}

func TestBuildUnifiedDiff_NoContentChange(t *testing.T) {
	diff, err := buildUnifiedDiff("test.txt", []byte("same\n"), []byte("same\n"))
	if err != nil {
		t.Fatalf("buildUnifiedDiff() error = %v", err)
	}
	if diff != noContentChangeDiffMessage {
		t.Fatalf("buildUnifiedDiff() = %q, want %q", diff, noContentChangeDiffMessage)
	}
}

func TestBuildUnifiedDiff_PreservesTrailingNewlineRemoval(t *testing.T) {
	diff, err := buildUnifiedDiff("test.txt", []byte("same\n"), []byte("same"))
	if err != nil {
		t.Fatalf("buildUnifiedDiff() error = %v", err)
	}

	for _, want := range []string{
		"--- a/test.txt",
		"+++ b/test.txt",
		" same",
		"+" + noNewlineAtEOFMarker,
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("buildUnifiedDiff() missing %q:\n%s", want, diff)
		}
	}
}

func TestBuildUnifiedDiff_PreservesTrailingNewlineAddition(t *testing.T) {
	diff, err := buildUnifiedDiff("test.txt", []byte("same"), []byte("same\n"))
	if err != nil {
		t.Fatalf("buildUnifiedDiff() error = %v", err)
	}

	for _, want := range []string{
		"--- a/test.txt",
		"+++ b/test.txt",
		" same",
		"-" + noNewlineAtEOFMarker,
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("buildUnifiedDiff() missing %q:\n%s", want, diff)
		}
	}
}

func TestBuildUnifiedDiff_UsesNormalizedDisplayPaths(t *testing.T) {
	diff, err := buildUnifiedDiff("/tmp/nested/example.txt", []byte("before\n"), []byte("after\n"))
	if err != nil {
		t.Fatalf("buildUnifiedDiff() error = %v", err)
	}

	for _, want := range []string{
		"--- a/tmp/nested/example.txt",
		"+++ b/tmp/nested/example.txt",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("buildUnifiedDiff() missing %q:\n%s", want, diff)
		}
	}
}

func TestDiffResult_SkipsPreviewForLargeFiles(t *testing.T) {
	before := bytes.Repeat([]byte("a"), maxDiffInputBytes+1)
	after := bytes.Repeat([]byte("b"), maxDiffInputBytes+1)

	result := DiffResult("big.txt", before, after)

	if !result.Silent {
		t.Fatal("expected large diff previews to be skipped silently")
	}
	if result.ForUser != "" {
		t.Fatalf("expected no user-facing preview when skipped, got %q", result.ForUser)
	}
	if !strings.Contains(result.ForLLM, diffPreviewSkippedMessage) {
		t.Fatalf("expected skipped-preview note, got %q", result.ForLLM)
	}
}

func TestDiffResult_TruncatesModelAndUserPreviews(t *testing.T) {
	after := []byte(strings.Repeat("abcd", maxUserDiffPreviewBytes/4) + "\n")

	result := DiffResult("preview.txt", []byte("before\n"), after)

	if result.Silent {
		t.Fatal("expected preview to remain visible below the input caps")
	}
	if !strings.Contains(result.ForUser, diffPreviewTruncatedNote) {
		t.Fatalf("expected truncated user preview note, got %q", result.ForUser)
	}
	for _, want := range []string{modelDiffTruncatedNote, "[DIFF TRUNCATED"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Fatalf("expected model diff to contain %q, got %q", want, result.ForLLM)
		}
	}
	if got := utf8.RuneCountInString(result.ForLLM); got > maxModelDiffRunes+512 {
		t.Fatalf("model diff is not bounded: %d runes", got)
	}
	if got := len(result.ForUser); got > maxUserDiffPreviewBytes+512 {
		t.Fatalf("user diff is not bounded: %d bytes", got)
	}
}

func TestDiffResult_KeepsFullUserPreviewBelowUserBudget(t *testing.T) {
	after := []byte("before\n" + strings.Repeat("x", maxModelDiffRunes+100) + "\n")

	result := DiffResult("model-only.txt", []byte("before\n"), after)

	if strings.Contains(result.ForUser, diffPreviewTruncatedNote) {
		t.Fatalf("expected user preview to fit its larger budget, got %q", result.ForUser)
	}
	if !strings.Contains(result.ForLLM, modelDiffTruncatedNote) {
		t.Fatalf("expected model diff to be cut at maxModelDiffRunes, got %q", result.ForLLM)
	}
	if result.ForLLM == result.ForUser {
		t.Fatal("expected different bounded views when only the model budget is exceeded")
	}
}

func TestDiffResultWithContextRedactor_RedactsOnlyModelContextLines(t *testing.T) {
	before := []byte("api_key = \"sk-live-secret\"\nvalue = 1\n")
	after := []byte("api_key = \"sk-live-secret\"\nvalue = 2\n")

	result := DiffResultWithContextRedactor("config.txt", before, after, func(line string) string {
		return strings.ReplaceAll(line, "sk-live-secret", "[FILTERED]")
	})

	if result == nil {
		t.Fatal("DiffResultWithContextRedactor() returned nil")
	}
	if !strings.Contains(result.ForLLM, "[FILTERED]") {
		t.Fatalf("expected redacted context line in the model diff:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "sk-live-secret") {
		t.Fatalf("secret must not reach the model diff:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "sk-live-secret") {
		t.Fatalf("user preview must keep the raw diff:\n%s", result.ForUser)
	}
	for _, want := range []string{"-value = 1", "+value = 2"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Fatalf("model diff missing changed line %q:\n%s", want, result.ForLLM)
		}
	}
}

func TestDiffResultWithContextRedactor_LeavesChangedLinesVerbatim(t *testing.T) {
	before := []byte("context\nold-secret\n")
	after := []byte("context\nnew-secret\n")

	var seen []string
	result := DiffResultWithContextRedactor("f.txt", before, after, func(line string) string {
		seen = append(seen, line)
		return strings.ReplaceAll(line, "secret", "[FILTERED]")
	})

	for _, want := range []string{"-old-secret", "+new-secret"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Fatalf("changed line %q must stay verbatim:\n%s", want, result.ForLLM)
		}
	}
	for _, line := range seen {
		if strings.Contains(line, "secret") {
			t.Fatalf("redactor must only receive context lines, got %q", line)
		}
	}
}

func TestDiffResultWithContextRedactor_NoMatchKeepsSharedString(t *testing.T) {
	result := DiffResultWithContextRedactor(
		"f.txt",
		[]byte("alpha\nbeta\n"),
		[]byte("alpha\ngamma\n"),
		func(line string) string {
			return line
		},
	)

	if result.ForLLM != result.ForUser {
		t.Fatalf(
			"expected a shared string when no context line changes, got\nForLLM: %q\nForUser: %q",
			result.ForLLM,
			result.ForUser,
		)
	}
}

func TestDiffResultWithContextRedactor_RedactsBeforeTruncation(t *testing.T) {
	var before, after strings.Builder
	for i := 0; i < 120; i++ {
		contextLine := "line " + strconv.Itoa(i) + " " + strings.Repeat("x", 160)
		before.WriteString(contextLine + "\n")
		if i%3 == 0 {
			after.WriteString("changed " + strconv.Itoa(i) + " " + strings.Repeat("y", 160) + "\n")
		} else {
			after.WriteString(contextLine + "\n")
		}
	}

	var sawMiddle bool
	result := DiffResultWithContextRedactor(
		"big.txt",
		[]byte(before.String()),
		[]byte(after.String()),
		func(line string) string {
			if strings.Contains(line, "line 62 ") {
				sawMiddle = true
			}
			return line
		},
	)

	if !strings.Contains(result.ForLLM, "[DIFF TRUNCATED") {
		t.Fatalf("expected a truncated model diff:\n%s", result.ForLLM)
	}
	if !sawMiddle {
		t.Fatal("redactor did not receive the context line omitted by the head+tail cut")
	}
	if strings.Contains(result.ForLLM, "line 62 ") {
		t.Fatalf(
			"the middle context line must be omitted from the bounded diff:\n%s",
			result.ForLLM,
		)
	}
}

func TestDiffDisplayPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "absolute path",
			path: "/tmp/example.txt",
			want: "tmp/example.txt",
		},
		{
			name: "relative path",
			path: "pkg/tools/fs/edit.go",
			want: "pkg/tools/fs/edit.go",
		},
		{
			name: "empty path",
			path: "",
			want: "file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := diffDisplayPath(tt.path); got != tt.want {
				t.Fatalf("diffDisplayPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
