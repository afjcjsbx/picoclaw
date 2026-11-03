package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func applyPatchToTempFile(t *testing.T, content, patch string) (*ToolResult, string) {
	t.Helper()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "target.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	tool := NewEditFileTool(workspace, true)
	result := tool.Execute(context.Background(), map[string]any{
		"path":  path,
		"patch": patch,
	})

	edited, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	return result, string(edited)
}

func TestEditFileTool_V4APatch_SingleHunk(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: target.txt
@@ func main() {
 func main() {
-	old()
+	new()
 }
*** End Patch`

	result, got := applyPatchToTempFile(t, "func main() {\n\told()\n}\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "func main() {\n\tnew()\n}\n", got)
	assert.Contains(t, result.ForUser, "+	new()")
	assert.Contains(t, result.ForUser, "-\told()")
}

func TestEditFileTool_V4APatch_MultipleHunks(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: target.txt
@@ first
 alpha
-beta
+BETA
@@ second
 delta
-epsilon
+EPSILON
*** End Patch`

	result, got := applyPatchToTempFile(t, "alpha\nbeta\ngamma\ndelta\nepsilon\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "alpha\nBETA\ngamma\ndelta\nEPSILON\n", got)
}

func TestEditFileTool_V4APatch_OutOfOrderHunks(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: target.txt
@@ second
 delta
-epsilon
+EPSILON
@@ first
 alpha
-beta
+BETA
*** End Patch`

	result, got := applyPatchToTempFile(t, "alpha\nbeta\ngamma\ndelta\nepsilon\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "alpha\nBETA\ngamma\ndelta\nEPSILON\n", got)
}

func TestEditFileTool_V4APatch_OutOfOrderAmbiguousContextFails(t *testing.T) {
	patch := `@@
-mid
+MID
@@
-dup
+DUP`

	result, got := applyPatchToTempFile(t, "dup\ndup\nmid\n", patch)

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "matches 2 times")
	assert.Equal(t, "dup\ndup\nmid\n", got, "file must not change on error")
}

func TestEditFileTool_V4APatch_WrapperWithoutHeader(t *testing.T) {
	patch := `*** Begin Patch
@@
 one
-two
+TWO
*** End Patch`

	result, got := applyPatchToTempFile(t, "one\ntwo\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "one\nTWO\n", got)
}

func TestEditFileTool_V4APatch_HunkBodyWithoutWrapper(t *testing.T) {
	patch := `@@
 one
-two
+TWO`

	result, got := applyPatchToTempFile(t, "one\ntwo\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "one\nTWO\n", got)
}

func TestEditFileTool_V4APatch_RejectsInsertionWithoutContext(t *testing.T) {
	result, got := applyPatchToTempFile(t, "one\ntwo\n", "+zero\n")

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "no context")
	assert.Equal(t, "one\ntwo\n", got, "file must not change on error")
}

func TestEditFileTool_V4APatch_RejectsHunkWithoutContext(t *testing.T) {
	result, got := applyPatchToTempFile(t, "one\ntwo\n", "@@\n+zero\n")

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "no context")
	assert.Equal(t, "one\ntwo\n", got, "file must not change on error")
}

func TestEditFileTool_V4APatch_AppendWithoutContextAtEndOfFile(t *testing.T) {
	tests := map[string]struct {
		content string
		patch   string
		want    string
	}{
		"trailing newline":    {"a\nb\n", "@@\n+tail\n*** End of File", "a\nb\ntail\n"},
		"no trailing newline": {"a\nb", "@@\n+tail\n*** End of File", "a\nb\ntail"},
		"blank final line":    {"a\n\n", "@@\n+tail\n*** End of File", "a\n\ntail\n"},
		"empty file":          {"", "@@\n+tail\n*** End of File", "tail\n"},
		"multiple lines":      {"a\n", "@@\n+c\n+d\n*** End of File", "a\nc\nd\n"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result, got := applyPatchToTempFile(t, tc.content, tc.patch)

			assert.False(t, result.IsError, result.ForLLM)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEditFileTool_V4APatch_AppendWithContext(t *testing.T) {
	patch := `@@
 b
+c`

	result, got := applyPatchToTempFile(t, "a\nb\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "a\nb\nc\n", got)
}

func TestEditFileTool_V4APatch_EndOfFile(t *testing.T) {
	patch := `@@
 b
+c
*** End of File`

	result, got := applyPatchToTempFile(t, "a\nb\n", patch)

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "a\nb\nc\n", got)
}

func TestEditFileTool_V4APatch_EndOfFileMismatch(t *testing.T) {
	patch := `@@
 b
+c
*** End of File`

	result, _ := applyPatchToTempFile(t, "a\nb\nz\n", patch)

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "end of the file")
}

func TestEditFileTool_V4APatch_ContentAfterEndOfFile(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: target.txt
@@
-old
+new
*** End of File
+extra
*** End Patch`

	result, got := applyPatchToTempFile(t, "old\n", patch)

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "unexpected content after")
	assert.Equal(t, "old\n", got)
}

func TestEditFileTool_V4APatch_ContextNotFound(t *testing.T) {
	result, _ := applyPatchToTempFile(t, "alpha\nbeta\n", "-missing\n+new\n")

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "context not found")
}

func TestEditFileTool_V4APatch_AmbiguousContext(t *testing.T) {
	result, _ := applyPatchToTempFile(t, "same\nsame\n", "-same\n+other\n")

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "matches 2 times")
}

func TestEditFileTool_V4APatch_TrailingWhitespaceTolerance(t *testing.T) {
	result, got := applyPatchToTempFile(t, "old\t \n", "-old\n+new\n")

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "new\n", got)
}

func TestEditFileTool_V4APatch_PreservesCRLF(t *testing.T) {
	result, got := applyPatchToTempFile(t, "a\r\nb\r\n", "-b\n+c\n")

	assert.False(t, result.IsError, result.ForLLM)
	assert.Equal(t, "a\r\nc\r\n", got)
}

func TestEditFileTool_V4APatch_HeaderMustMatchPath(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: other.txt
-old
+new
*** End Patch`

	result, got := applyPatchToTempFile(t, "old\n", patch)

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, `patch targets "other.txt"`)
	assert.Equal(t, "old\n", got, "file must not change on error")
}

func TestEditFileTool_V4APatch_RejectsUnsupportedSections(t *testing.T) {
	tests := map[string]string{
		"add file":    "*** Begin Patch\n*** Add File: new.txt\n+content\n*** End Patch",
		"delete file": "*** Begin Patch\n*** Delete File: target.txt\n*** End Patch",
		"move to":     "*** Begin Patch\n*** Update File: target.txt\n*** Move to: new.txt\n*** End Patch",
	}

	for name, patch := range tests {
		t.Run(name, func(t *testing.T) {
			result, got := applyPatchToTempFile(t, "old\n", patch)

			assert.True(t, result.IsError)
			assert.Contains(t, result.ForLLM, "not supported by edit_file")
			assert.Equal(t, "old\n", got)
		})
	}
}

func TestEditFileTool_V4APatch_RequiresEndPatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: target.txt\n-old\n+new\n"

	result, got := applyPatchToTempFile(t, "old\n", patch)

	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "missing \"*** End Patch\"")
	assert.Equal(t, "old\n", got)
}

func TestEditFileTool_V4APatch_RejectsEmptyAndMixedArguments(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "target.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFileTool(workspace, true)

	result := tool.Execute(context.Background(), map[string]any{"path": path, "patch": "  "})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "patch is empty")

	result = tool.Execute(context.Background(), map[string]any{
		"path":     path,
		"patch":    "-old\n+new\n",
		"old_text": "old",
		"new_text": "new",
	})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "not both")
}

func TestEditFileTool_V4APatch_MissingFile(t *testing.T) {
	workspace := t.TempDir()
	tool := NewEditFileTool(workspace, true)

	result := tool.Execute(context.Background(), map[string]any{
		"path":  filepath.Join(workspace, "missing.txt"),
		"patch": "-old\n+new\n",
	})

	assert.True(t, result.IsError)
	assert.True(t, strings.Contains(result.ForLLM, "no such file") || strings.Contains(result.ForLLM, "not found"))
}
