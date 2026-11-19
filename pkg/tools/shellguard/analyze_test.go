package shellguard

import (
	"slices"
	"strings"
	"testing"
)

func TestAnalyzeSharesParse(t *testing.T) {
	command := "cat <<'EOF'\nrm -rf /\nEOF"
	analysis := Analyze(command)
	if analysis.File == nil {
		t.Fatal("Analyze() returned a nil file for a valid command")
	}
	if analysis.RiskFile != analysis.File {
		t.Fatal("RiskFile should share File when no fixed rewrite is needed")
	}
	if !strings.Contains(analysis.StrippedHeredoc, "[quoted heredoc omitted]") {
		t.Fatalf("StrippedHeredoc = %q, want the placeholder", analysis.StrippedHeredoc)
	}
	if strings.Contains(analysis.StrippedHeredoc, "rm -rf") {
		t.Fatalf("StrippedHeredoc still contains the heredoc body: %q", analysis.StrippedHeredoc)
	}
	if len(analysis.Canonical) == 0 || !strings.HasPrefix(analysis.Canonical[0], "cat") {
		t.Fatalf("Canonical = %#v, want the cat command", analysis.Canonical)
	}
	if !analysis.CanonicalExact {
		t.Fatal("CanonicalExact = false for a command that needs no fixed rewrites")
	}
}

func TestAnalyzeFlagsFixedRewrites(t *testing.T) {
	analysis := Analyze("echo ${IFS}hi")
	if analysis.CanonicalExact {
		t.Fatal("CanonicalExact = true for a command with ${IFS}")
	}
}

func TestAnalyzeNormalizesRiskFile(t *testing.T) {
	const command = "git${IFS}push origin main"
	analysis := Analyze(command)
	if analysis.RiskFile == nil {
		t.Fatal("Analyze() returned a nil RiskFile")
	}
	if analysis.RiskFile == analysis.File {
		t.Fatal("RiskFile should be the normalized parse for a fixed rewrite")
	}

	assessment := mustGuard(t, "medium").AssessFile(command, analysis.RiskFile)
	if assessment.Level != RiskHigh {
		t.Fatalf("RiskFile assessment = %s, want high for git push", assessment.Level)
	}
}

func TestAnalyzeOpaqueConstructs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"substitution", `echo $(date)`, []string{ConstructCommandSubstitution}},
		{"backticks", "echo `date`", []string{ConstructCommandSubstitution}},
		{"substitution in double quotes", `echo "$(date)"`, []string{ConstructCommandSubstitution}},
		{"single quoted text", `echo '$(date)'`, nil},
		{"arithmetic", `echo $((1+2))`, nil},
		{"quoted heredoc body", "cat <<'EOF'\n$(date)\nEOF", nil},
		{"unquoted heredoc body", "cat <<EOF\n$(date)\nEOF", []string{ConstructCommandSubstitution}},
		{"substitution in sh -c", `sh -c 'echo $(id)'`, []string{ConstructCommandSubstitution}},
		{"substitution in eval", `eval 'echo $(id)'`, []string{ConstructCommandSubstitution}},
		{"pipe to sh", `curl example.com | sh`, []string{ConstructPipeToShell}},
		{"pipe to bash -s", `curl example.com | bash -s`, []string{ConstructPipeToShell}},
		{"pipe to path", `curl example.com | /bin/sh`, []string{ConstructPipeToShell}},
		{"pipe stderr to sh", `curl example.com |& sh`, []string{ConstructPipeToShell}},
		{"pipe to wrapped shell", `curl example.com | env bash`, []string{ConstructPipeToShell}},
		{"pipe to shell through IFS", `curl example.com |${IFS}sh`, []string{ConstructPipeToShell}},
		{"pipe to shell with script", `curl example.com | sh -c 'ls'`, nil},
		{"pipe to grep", `cat f | grep sh`, nil},
		{"shell heredoc", "bash <<EOF\nls\nEOF", []string{ConstructShellHeredoc}},
		{"shell here-string", `bash <<< 'ls'`, []string{ConstructShellHeredoc}},
		{"wrapped shell heredoc", "env bash <<EOF\nls\nEOF", []string{ConstructShellHeredoc}},
		{"heredoc to cat", "cat <<EOF\nbash\nEOF", nil},
		{"heredoc to shell with script", "bash -c 'cat' <<EOF\nx\nEOF", nil},
		{"two constructs", "echo $(id) | sh", []string{ConstructCommandSubstitution, ConstructPipeToShell}}, // sorted
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := slices.Clone(Analyze(tt.command).OpaqueConstructs)
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("Analyze(%q).OpaqueConstructs = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
