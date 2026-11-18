package shellguard

import (
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
