package fstools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuickSyntaxCheck(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		checked bool
		skipped bool
		problem string
	}{
		{
			name:    "valid JSON",
			path:    "config.json",
			content: `{"a":[1,2]}`,
			checked: true,
		},
		{
			name:    "invalid JSON",
			path:    "config.json",
			content: `{"a":}`,
			checked: true,
			problem: "not strict JSON",
		},
		{
			name:    "tsconfig with comments is not checked",
			path:    "tsconfig.json",
			content: "{\n  // comment\n  \"compilerOptions\": {}\n}\n",
			checked: false,
		},
		{
			name:    "named tsconfig with comments is not checked",
			path:    "web/tsconfig.app.json",
			content: "{\n  // comment\n  \"compilerOptions\": {}\n}\n",
			checked: false,
		},
		{
			name:    "vscode settings with comments is not checked",
			path:    ".vscode/settings.json",
			content: "{\n  // comment\n  \"editor.tabSize\": 2\n}\n",
			checked: false,
		},
		{
			name:    "ordinary JSON with comments is still flagged",
			path:    "data.json",
			content: "{\n  // comment\n  \"a\": 1\n}\n",
			checked: true,
			problem: "not strict JSON",
		},
		{
			name:    "valid Go with strings comments and raw strings",
			path:    "main.go",
			content: "package main\n\n// } ignored\nvar s = \"}\" // }\nvar r = `{\"//`\nvar c = '}'\nfunc main() { println(s, r, c) }\n",
			checked: true,
		},
		{
			name:    "unclosed Go brace",
			path:    "main.go",
			content: "package main\n\nfunc main() {\n",
			checked: true,
			problem: "unclosed '{' opened at line 3",
		},
		{
			name:    "unexpected closer reports line",
			path:    "main.go",
			content: "package main\nvar x = }\n",
			checked: true,
			problem: "unexpected '}' at line 2",
		},
		{
			name:    "mismatched closer",
			path:    "main.go",
			content: "var x = (]\n",
			checked: true,
			problem: "mismatched ']' at line 1 (opened '(')",
		},
		{
			name:    "unterminated Go string",
			path:    "main.go",
			content: "var s = \"abc\n",
			checked: true,
			problem: "unterminated string starting at line 1",
		},
		{
			name:    "unterminated Go raw string",
			path:    "main.go",
			content: "var s = `abc\n",
			checked: true,
			problem: "unterminated raw string starting at line 1",
		},
		{
			name:    "unterminated block comment reports start line",
			path:    "main.go",
			content: "package main\n/* never closed\n",
			checked: true,
			problem: "unterminated block comment starting at line 2",
		},
		{
			name:    "valid Python with triple quoted string",
			path:    "script.py",
			content: "x = 1  # } ignored\ns = \"\"\"a { b # c\"\"\"\nprint(s)\n",
			checked: true,
		},
		{
			name:    "Python raw string keeps escaped quote",
			path:    "script.py",
			content: "s = r\"\\\"\"\n",
			checked: true,
		},
		{
			name:    "Python raw string with trailing backslash is unterminated",
			path:    "script.py",
			content: "path = r\"C:\\\"\n",
			checked: true,
			problem: "unterminated string starting at line 1",
		},
		{
			name:    "Python escaped quote",
			path:    "script.py",
			content: "s = 'It\\'s'\n",
			checked: true,
		},
		{
			name:    "valid C++ digit separators",
			path:    "main.cpp",
			content: "int a = 1'000;\nint b = 0xDEAD'BEEF;\n",
			checked: true,
		},
		{
			name:    "C++ char literal after a digit still parses",
			path:    "main.cpp",
			content: "int x = 1 + 'a';\n",
			checked: true,
		},
		{
			name:    "TOML literal string keeps backslash",
			path:    "config.toml",
			content: "path = 'C:\\'\n",
			checked: true,
		},
		{
			name:    "unterminated Python triple quote",
			path:    "script.py",
			content: "s = \"\"\"never closed\n",
			checked: true,
			problem: "unterminated string starting at line 1",
		},
		{
			name:    "valid TOML arrays",
			path:    "config.toml",
			content: "[[products]]\nname = \"x\"\n",
			checked: true,
		},
		{
			name:    "TOML multiline string closes with four quotes",
			path:    "config.toml",
			content: "a = \"\"\"x\"\"\"\"\n",
			checked: true,
		},
		{
			name:    "TOML multiline string closes with five quotes",
			path:    "config.toml",
			content: "a = \"\"\"x\"\"\"\"\"\n",
			checked: true,
		},
		{
			name:    "Python triple string with five closing quotes",
			path:    "script.py",
			content: "x = \"\"\"a\"\"\"\"\"\n",
			checked: true,
		},
		{
			name:    "unknown extension is not checked",
			path:    "notes.txt",
			content: "unbalanced {",
			checked: false,
		},
		{
			name:    "nesting past the fixed stack skips the check",
			path:    "main.go",
			content: strings.Repeat("(", maxSyntaxDepth+1),
			checked: true,
			skipped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := quickSyntaxCheck(tt.path, []byte(tt.content))
			assert.Equal(t, tt.checked, check.Checked)
			assert.Equal(t, tt.skipped, check.Skipped)
			assert.Equal(t, tt.problem, check.Problem)
		})
	}
}
