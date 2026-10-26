package seahorse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8BytesPreservesRunes(t *testing.T) {
	// 1000 CJK runes = 3000 bytes; the 2048-byte cut would land mid-rune.
	in := strings.Repeat("会", 1000)
	out := truncateUTF8Bytes(in, maxTruncatedSummaryBytes)
	if len(out) > maxTruncatedSummaryBytes {
		t.Fatalf("len(out) = %d, want <= %d", len(out), maxTruncatedSummaryBytes)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("truncateUTF8Bytes produced invalid UTF-8")
	}
}

func TestTruncateSummaryValidUTF8(t *testing.T) {
	out := truncateSummary([]Message{{Content: strings.Repeat("語", 1000)}})
	if !utf8.ValidString(out) {
		t.Fatalf("truncateSummary produced invalid UTF-8")
	}
}

func TestTruncateCondensedSummariesValidUTF8(t *testing.T) {
	out := truncateCondensedSummaries([]Summary{{Content: strings.Repeat("語", 1000)}})
	if !utf8.ValidString(out) {
		t.Fatalf("truncateCondensedSummaries produced invalid UTF-8")
	}
}
