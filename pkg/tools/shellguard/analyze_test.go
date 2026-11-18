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

func FuzzAnalyzeHardlineAndRisk(f *testing.F) {
	seeds := []string{
		"rm -rf /",
		"bomb(){ bomb|bomb& };bomb",
		"find / -delete",
		"python3 script.py",
		"sh -c 'rm -rf /'",
		"kill -9 1",
		"echo hi",
		"echo )",
		"$'\\x72\\x6d' -rf /",
		"env -S 'rm -rf /'",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	guard, err := NewGuard("medium", nil, nil)
	if err != nil {
		f.Fatalf("NewGuard: %v", err)
	}

	f.Fuzz(func(t *testing.T, command string) {
		analysis := Analyze(command)
		if analysis.File != nil {
			_, _ = HardlineFile(analysis.File, "/workspace", "/home/user")
		}
		if analysis.RiskFile != nil {
			_, _ = HardlineFile(analysis.RiskFile, "/workspace", "/home/user")
		}
		// The classifier must not panic on arbitrary input. Whether the result
		// is allow, review or block is covered by the deterministic tests.
		_ = guard.AssessFile(command, analysis.RiskFile)
	})
}
