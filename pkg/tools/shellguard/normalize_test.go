package shellguard

import "testing"

func TestNormalizeForDeny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "ifs braces",
			command: `rm${IFS}-rf${IFS}/`,
			want:    `rm -rf /`,
		},
		{
			name:    "ifs bare",
			command: `rm$IFS-rf$IFS/`,
			want:    `rm -rf /`,
		},
		{
			name:    "backslash newline",
			command: "r\\\nm -rf /",
			want:    "rm -rf /",
		},
		{
			name:    "backslash crlf",
			command: "r\\\r\nm -rf /",
			want:    "rm -rf /",
		},
		{
			name:    "ansi hex command name",
			command: `$'\x72\x6d' -rf /`,
			want:    `rm -rf /`,
		},
		{
			name:    "ansi hex inside word",
			command: `r$'\x6d' -rf /`,
			want:    `rm -rf /`,
		},
		{
			name:    "ansi octal",
			command: `$'\162\155' -rf /`,
			want:    `rm -rf /`,
		},
		{
			name:    "ansi unicode",
			command: `$'\u0072\u006d' -rf /`,
			want:    `rm -rf /`,
		},
		{
			name:    "ansi inside double quotes is literal",
			command: `echo "$'\x72'"`,
			want:    `echo "$'\x72'"`,
		},
		{
			name:    "plain command unchanged",
			command: `git status`,
			want:    `git status`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeForDeny(tt.command); got != tt.want {
				t.Fatalf("NormalizeForDeny(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
