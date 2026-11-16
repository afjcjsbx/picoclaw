package shellguard

import (
	"slices"
	"testing"
)

func TestCanonicalCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "quote spliced command name",
			command: `r''m -rf /`,
			want:    []string{"rm -rf /"},
		},
		{
			name:    "double quote spliced command name",
			command: `r""m -rf /`,
			want:    []string{"rm -rf /"},
		},
		{
			name:    "quote spliced pipeline",
			command: `echo hi | g''it push origin main`,
			want:    []string{"echo hi", "git push origin main"},
		},
		{
			name:    "backslash escaped command name",
			command: `r\m -rf /`,
			want:    []string{"rm -rf /"},
		},
		{
			name:    "backslash escaped letters",
			command: `\r\m -rf /`,
			want:    []string{"rm -rf /"},
		},
		{
			name:    "double backslash is unescaped once",
			command: `r\\m -rf /`,
			want:    []string{`r\m -rf /`},
		},
		{
			name:    "triple backslash is unescaped once",
			command: `r\\\m -rf /`,
			want:    []string{`r\m -rf /`},
		},
		{
			name:    "double quoted backslash is literal",
			command: `"r\m" -rf /`,
			want:    []string{`r\m -rf /`},
		},
		{
			name:    "single quoted backslash is literal",
			command: `'r\m' -rf /`,
			want:    []string{`r\m -rf /`},
		},
		{
			name:    "ansi unknown escape keeps backslash",
			command: `$'r\m' -rf /`,
			want:    []string{`r\m -rf /`},
		},
		{
			name:    "ansi hex escape decodes once",
			command: `$'r\x6d' -rf /`,
			want:    []string{"rm -rf /"},
		},
		{
			name:    "nested single quoted backslash script",
			command: `sh -c 'r\m -rf /'`,
			want:    []string{`sh -c r\m -rf /`, "rm -rf /"},
		},
		{
			name:    "nested double quoted backslash script",
			command: `sh -c "r\m -rf /"`,
			want:    []string{`sh -c r\m -rf /`, "rm -rf /"},
		},
		{
			name:    "eval backslash script",
			command: `eval 'r\m -rf /'`,
			want:    []string{`eval r\m -rf /`, "rm -rf /"},
		},
		{
			name:    "trap backslash action",
			command: `trap 'r\m -rf /' EXIT`,
			want:    []string{`trap r\m -rf / EXIT`, "rm -rf /"},
		},
		{
			name:    "nested command substitution",
			command: `echo $(r''m -rf /)`,
			want:    []string{"echo", "rm -rf /"},
		},
		{
			name:    "dynamic command name skipped",
			command: `$CMD -rf /`,
			want:    nil,
		},
		{
			name:    "dynamic argument keeps literal prefix",
			command: `g''it push "$BRANCH"`,
			want:    []string{"git push"},
		},
		{
			name:    "quoted redirection target",
			command: `echo x >/dev/s''da`,
			want:    []string{"echo x >/dev/sda"},
		},
		{
			name:    "nested quoted shell script",
			command: `sh -c "r''m -rf /"`,
			want:    []string{`sh -c r''m -rf /`, "rm -rf /"},
		},
		{
			name:    "nested ansi shell script",
			command: `sh -c "$'\x72\x6d' -rf /"`,
			want:    []string{`sh -c $'\x72\x6d' -rf /`, "rm -rf /"},
		},
		{
			name:    "wrapped quoted shell script",
			command: `env sh -c "r''m -rf /"`,
			want:    []string{`env sh -c r''m -rf /`, `sh -c r''m -rf /`, "rm -rf /"},
		},
		{
			name:    "eval quoted script",
			command: `eval "r''m -rf /"`,
			want:    []string{`eval r''m -rf /`, "rm -rf /"},
		},
		{
			name:    "trap quoted action",
			command: `trap "r''m -rf /" EXIT`,
			want:    []string{`trap r''m -rf / EXIT`, "rm -rf /"},
		},
		{
			name:    "trap print does not inspect signals",
			command: `trap -p SIGINT`,
			want:    []string{"trap -p SIGINT"},
		},
		{
			name:    "heredoc body is not a command",
			command: "cat <<'EOF'\nr''m -rf /\nEOF",
			want:    []string{"cat"},
		},
		{
			name:    "invalid syntax",
			command: `echo "unterminated`,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CanonicalCommands(tt.command); !slices.Equal(got, tt.want) {
				t.Fatalf("CanonicalCommands(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
