package toolshared

import (
	"mime"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	LargeBase64OmittedMessage = "[Tool returned a large base64-like payload; omitted from model context.]"
	InlineMediaOmittedMessage = "[Tool returned inline media content; omitted from model context.]"
)

var (
	inlineMarkdownDataURLRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^)]+)\)`)
	inlineRawDataURLRe      = regexp.MustCompile(`data:[^;\s]+;base64,[A-Za-z0-9+/=\r\n]+`)
)

// InlineDataURL is one data: URL found in tool output. Full is the complete
// match — what callers remove from the text — while URL is the data: URL
// itself, e.g. the capture of a markdown image.
type InlineDataURL struct {
	Full string
	URL  string
}

// MarkdownDataURLs returns the data URLs used as markdown image sources.
func MarkdownDataURLs(text string) []InlineDataURL {
	matches := inlineMarkdownDataURLRe.FindAllStringSubmatch(text, -1)
	found := make([]InlineDataURL, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		found = append(found, InlineDataURL{Full: match[0], URL: match[1]})
	}
	return found
}

// RawBase64DataURLs returns the bare base64 data: URLs in text, i.e. the ones
// that are not wrapped in a markdown image.
func RawBase64DataURLs(text string) []InlineDataURL {
	matches := inlineRawDataURLRe.FindAllString(text, -1)
	found := make([]InlineDataURL, 0, len(matches))
	for _, match := range matches {
		found = append(found, InlineDataURL{Full: match, URL: match})
	}
	return found
}

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

	// Base64 classes are all ASCII, so a byte scan is enough. The space
	// budget is enforced while scanning: once it is exceeded the heuristic
	// can no longer match, which lets ordinary output (code, logs, JSON)
	// bail out after the first few kilobytes instead of walking the whole
	// payload rune by rune.
	maxSpaces := len(trimmed) / 128
	nonSpace := 0
	base64Like := 0
	spaceCount := 0
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			spaceCount++
			if spaceCount > maxSpaces {
				return false
			}
			continue
		}
		nonSpace++
		if (c >= 'A' && c <= 'Z') ||
			(c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') ||
			c == '+' || c == '/' || c == '=' {
			base64Like++
		}
	}
	return nonSpace > 0 && float64(base64Like)/float64(nonSpace) >= 0.97 && spaceCount <= maxSpaces
}

func ExtensionForMIMEType(mimeType string) string {
	if mimeType == "" {
		return ".bin"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	switch strings.ToLower(mimeType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	default:
		return filepath.Ext(mimeType)
	}
}
