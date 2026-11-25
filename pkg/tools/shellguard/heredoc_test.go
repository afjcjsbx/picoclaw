package shellguard

import (
	"strings"
	"testing"
)

func TestStripQuotedHeredocBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		wantHidden bool
	}{
		{
			name:       "single quoted",
			command:    "cat <<'EOF'\nrm -rf /\nEOF",
			wantHidden: true,
		},
		{
			name:       "double quoted",
			command:    "cat <<\"EOF\"\nrm -rf /\nEOF",
			wantHidden: true,
		},
		{
			name:       "backslash quoted",
			command:    "cat <<\\EOF\nrm -rf /\nEOF",
			wantHidden: true,
		},
		{
			name:       "tab stripped",
			command:    "cat <<-'EOF'\n\trm -rf /\n\tEOF",
			wantHidden: true,
		},
		{
			name:       "multiple bodies",
			command:    "cat <<'A'\nfirst\nA\ncat <<'B'\nsecond\nB",
			wantHidden: true,
		},
		{
			name:    "unquoted body expands",
			command: "cat <<EOF\n$(rm -rf /)\nEOF",
		},
		{
			name:    "lookalike inside double quotes",
			command: "echo \"<<'EOF'\"\nchmod 777 /tmp/x\nEOF",
		},
		{
			name:    "lookalike inside comment",
			command: "echo ok # <<'EOF'\nchmod 777 /tmp/x\nEOF",
		},
		{
			name:    "parse error",
			command: "cat <<'EOF'\nrm -rf /\nEOF\n)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Analyze(tt.command).StrippedHeredoc
			hidden := strings.Contains(got, "quoted heredoc omitted")
			if hidden != tt.wantHidden {
				t.Fatalf("Analyze(%q).StrippedHeredoc = %q, hidden=%v want %v",
					tt.command, got, hidden, tt.wantHidden)
			}
			if !tt.wantHidden {
				return
			}
			for _, body := range []string{"rm -rf /", "first", "second"} {
				if strings.Contains(got, body) {
					t.Fatalf("body content survived stripping: %q", got)
				}
			}
		})
	}
}

func TestStripQuotedHeredocBodiesPreservesClosingDelimiter(t *testing.T) {
	t.Parallel()

	command := "cat <<'EOF'\nrm -rf /\nEOF\n"
	got := Analyze(command).StrippedHeredoc
	if !strings.HasSuffix(got, "EOF\n") {
		t.Fatalf("closing delimiter lost: %q", got)
	}
}
