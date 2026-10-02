package toolshared

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	LargeBase64OmittedMessage = "[Tool returned a large base64-like payload; omitted from model context.]"
	InlineMediaOmittedMessage = "[Tool returned inline media content; omitted from model context.]"
)

var (
	inlineMarkdownDataURLRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^)]+)\)`)
	inlineRawDataURLRe      = regexp.MustCompile(`data:[^;\s]+;base64,[A-Za-z0-9+/=\r\n]+`)
)

func SanitizeToolLLMContent(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	if inlineMarkdownDataURLRe.MatchString(trimmed) || inlineRawDataURLRe.MatchString(trimmed) {
		cleaned := inlineMarkdownDataURLRe.ReplaceAllString(trimmed, "")
		cleaned = inlineRawDataURLRe.ReplaceAllString(cleaned, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			return InlineMediaOmittedMessage
		}
		return cleaned + "\n" + InlineMediaOmittedMessage
	}
	if looksLikeLargeBase64Payload(trimmed) {
		return LargeBase64OmittedMessage
	}
	return text
}

func looksLikeLargeBase64Payload(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 1024 {
		return false
	}

	nonSpace := 0
	base64Like := 0
	spaceCount := 0
	for _, r := range trimmed {
		if unicode.IsSpace(r) {
			spaceCount++
			continue
		}
		nonSpace++
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=' {
			base64Like++
		}
	}
	return nonSpace > 0 && float64(base64Like)/float64(nonSpace) >= 0.97 && spaceCount <= len(trimmed)/128
}
