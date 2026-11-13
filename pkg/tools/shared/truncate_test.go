package toolshared

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestTruncateHeadTail_NoCutWhenItFits(t *testing.T) {
	text := "short output"
	got, cut := TruncateHeadTail(text, 100, "OUTPUT")
	if cut {
		t.Fatalf("expected no truncation, got %q", got)
	}
	if got != text {
		t.Fatalf("expected text unchanged, got %q", got)
	}

	if got, cut := TruncateHeadTail(text, len(text), "OUTPUT"); cut || got != text {
		t.Fatalf("expected exact fit to stay unchanged, got cut=%v %q", cut, got)
	}
	if got, cut := TruncateHeadTail(text, 0, "OUTPUT"); cut || got != text {
		t.Fatalf("expected maxRunes=0 to disable truncation, got cut=%v %q", cut, got)
	}
}

func TestTruncateHeadTail_HeadNoticeTail(t *testing.T) {
	text := strings.Repeat("ab", 5000) // 10,000 runes
	got, cut := TruncateHeadTail(text, 1000, "OUTPUT")
	if !cut {
		t.Fatal("expected truncation")
	}

	want := strings.Repeat("ab", 200) + // 40% head = 400 runes
		"\n\n... [OUTPUT TRUNCATED - 9,000 chars omitted out of 10,000 total] ...\n\n" +
		strings.Repeat("ab", 300) // 60% tail = 600 runes
	if got != want {
		t.Fatalf("unexpected truncation output:\n got: %q\nwant: %q", got, want)
	}
}

func TestTruncateHeadTail_RuneSafe(t *testing.T) {
	text := strings.Repeat("è", 100) // 2-byte runes
	got, cut := TruncateHeadTail(text, 10, "CONTENT")
	if !cut {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
	if !strings.Contains(got, "[CONTENT TRUNCATED - 90 chars omitted out of 100 total]") {
		t.Fatalf("missing notice in %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("è", 4)) { // 40% of 10 = 4 runes
		t.Fatalf("missing head in %q", got)
	}
	if !strings.HasSuffix(got, strings.Repeat("è", 6)) { // 60% of 10 = 6 runes
		t.Fatalf("missing tail in %q", got)
	}
}

// TestTruncateHeadTail_DoesNotSplitSecrets verifies that a credential crossing
// either cut is dropped whole instead of leaving a prefix or suffix fragment.
func TestTruncateHeadTail_DoesNotSplitSecrets(t *testing.T) {
	const secret = "sk-live-0123456789abcdef"
	redact.SetSecrets([]string{secret})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	headText := strings.Repeat("a", 35) + secret + strings.Repeat("b", 200)
	tailText := strings.Repeat("a", 200) + secret + strings.Repeat("b", 45)

	for _, text := range []string{headText, tailText} {
		got, cut := TruncateHeadTail(text, 100, "OUTPUT")
		if !cut {
			t.Fatal("expected truncation")
		}
		for i := 0; i+4 <= len(secret); i++ {
			if strings.Contains(got, secret[i:i+4]) {
				t.Fatalf("credential fragment %q leaked: %q", secret[i:i+4], got)
			}
		}
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		5:       "5",
		999:     "999",
		1000:    "1,000",
		48166:   "48,166",
		1234567: "1,234,567",
	}
	for n, want := range cases {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}
