package toolshared

import (
	"strings"
	"testing"
)

func TestSanitizeToolLLMContent(t *testing.T) {
	rawDataURL := "data:image/png;base64," + strings.Repeat("A", 64)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text is untouched",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "surrounding whitespace is kept when no media is present",
			in:   "  spaced  ",
			want: "  spaced  ",
		},
		{
			name: "data scheme mention without an inline URL is untouched",
			in:   "the data: field is set by the caller",
			want: "the data: field is set by the caller",
		},
		{
			name: "markdown image data URL is removed",
			in:   "before ![alt](data:image/png;base64,AAAA) after",
			want: "before  after\n" + InlineMediaOmittedMessage,
		},
		{
			name: "raw data URL is removed",
			in:   "output " + rawDataURL + " trailing",
			want: "output  trailing\n" + InlineMediaOmittedMessage,
		},
		{
			name: "only a data URL leaves the omission message",
			in:   "![alt](data:image/png;base64,AAAA)",
			want: InlineMediaOmittedMessage,
		},
		{
			name: "large base64-like payload is omitted",
			in:   strings.Repeat("A", 2048),
			want: LargeBase64OmittedMessage,
		},
		{
			name: "large whitespace-rich text is not base64",
			in:   strings.Repeat("func main() { return nil } // comment\n", 64),
			want: strings.Repeat("func main() { return nil } // comment\n", 64),
		},
		{
			name: "large punctuation-rich payload is not base64",
			in:   strings.Repeat(`{"key":"value"},`, 128),
			want: strings.Repeat(`{"key":"value"},`, 128),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeToolLLMContent(tt.in); got != tt.want {
				t.Fatalf("SanitizeToolLLMContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func BenchmarkSanitizeToolLLMContent_Plain1MB(b *testing.B) {
	payload := strings.Repeat("tool output line with some words\n", 32768)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeToolLLMContent(payload)
	}
}

func BenchmarkSanitizeToolLLMContent_WithDataURL(b *testing.B) {
	payload := strings.Repeat("tool output line with some words\n", 32768) +
		"![img](data:image/png;base64,AAAA)"
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeToolLLMContent(payload)
	}
}

func BenchmarkSanitizeToolLLMContent_Base64Blob(b *testing.B) {
	payload := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo+/=", 30000)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = SanitizeToolLLMContent(payload)
	}
}
