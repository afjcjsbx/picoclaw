package routing

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/afjcjsbx/picoclaw/pkg/providers"
)

// lookbackWindow is the number of recent history entries scanned for tool calls.
// Six entries covers roughly one full tool-use round-trip (user → assistant+tool_call → tool_result → assistant).
const lookbackWindow = 6

// Features holds the structural signals extracted from a message and its session context.
// Every dimension is language-agnostic by construction — no keyword or pattern matching
// against natural-language content. This ensures consistent routing for all locales.
type Features struct {
	// TokenEstimate is a proxy for token count.
	// CJK runes count as 1 token each; non-CJK runes as 0.25 tokens each.
	// This avoids API calls while giving accurate estimates for all scripts.
	TokenEstimate int

	// CodeBlockCount is the number of fenced code blocks (``` pairs) in the message.
	// Coding tasks almost always require the heavy model.
	CodeBlockCount int

	// RecentToolCalls is the count of tool_call messages in the last lookbackWindow
	// history entries. A high density indicates an active agentic workflow.
	RecentToolCalls int

	// ConversationDepth is the total number of messages in the session history.
	// Deep sessions tend to carry implicit complexity built up over many turns.
	ConversationDepth int

	// HasAttachments is true when the message appears to contain media (images,
	// audio, video). Multi-modal inputs require vision-capable heavy models.
	HasAttachments bool
}

// ExtractFeatures computes the structural feature vector for a message.
// It is a pure function with no side effects. It makes no model or network
// calls; the only allocations are small string buffers for attachment
// detection (lowercasing, and stripping fenced code blocks when present).
func ExtractFeatures(msg string, history []providers.Message) Features {
	return Features{
		TokenEstimate:     estimateTokens(msg),
		CodeBlockCount:    countCodeBlocks(msg),
		RecentToolCalls:   countRecentToolCalls(history),
		ConversationDepth: len(history),
		HasAttachments:    hasAttachments(msg),
	}
}

// estimateTokens returns a token count proxy that handles both CJK and Latin text.
// CJK runes (U+2E80–U+9FFF, U+F900–U+FAFF, U+AC00–U+D7AF) map to roughly one
// token each, while non-CJK runes average ~0.25 tokens/rune (≈4 chars per token
// for English). Splitting the count this way avoids the 3x underestimation that a
// flat rune_count/3 would produce for Chinese, Japanese, and Korean text.
func estimateTokens(msg string) int {
	total := utf8.RuneCountInString(msg)
	if total == 0 {
		return 0
	}
	cjk := 0
	for _, r := range msg {
		if r >= 0x2E80 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF || r >= 0xAC00 && r <= 0xD7AF {
			cjk++
		}
	}
	return cjk + (total-cjk)/4
}

// countCodeBlocks counts the number of complete fenced code blocks.
// It recognizes both backtick (```) and tilde (~~~) fences; a block is closed
// only by a fence of the same type that opened it, so "```" followed by "~~~"
// is not a block. An unclosed opening fence is treated as zero complete blocks
// since it may just be an inline code span or a typo.
func countCodeBlocks(msg string) int {
	blocks := 0
	for rest := msg; ; {
		i, marker := nextFence(rest)
		if i < 0 {
			return blocks
		}
		rest = rest[i+len(marker):]
		end := strings.Index(rest, marker)
		if end < 0 {
			return blocks
		}
		blocks++
		rest = rest[end+len(marker):]
	}
}

// nextFence returns the index and marker (``` or ~~~) of the earliest fence
// marker in s, or -1 if there is none. Markers are matched anywhere in the
// text, not only at the start of a line, so "Try this: ```go ... ```" counts.
func nextFence(s string) (int, string) {
	b, t := strings.Index(s, "```"), strings.Index(s, "~~~")
	switch {
	case b < 0 && t < 0:
		return -1, ""
	case t < 0 || (b >= 0 && b < t):
		return b, "```"
	default:
		return t, "~~~"
	}
}

// countRecentToolCalls counts messages with tool calls in the last lookbackWindow
// entries of history. It examines the ToolCalls field rather than parsing
// the content string, so it is robust to any message format.
func countRecentToolCalls(history []providers.Message) int {
	start := len(history) - lookbackWindow
	if start < 0 {
		start = 0
	}

	count := 0
	for _, msg := range history[start:] {
		if len(msg.ToolCalls) > 0 {
			count += len(msg.ToolCalls)
		}
	}
	return count
}

// mediaExtensionRegex matches a filename-like token ending in a media
// extension. It requires a leading alphanumeric character and a trailing
// boundary so that a bare ".png" fragment or a longer token such as
// "photo.jpgx" does not look like an attachment. It is case-sensitive: callers
// must lowercase the input first (hasAttachments does).
var mediaExtensionRegex = regexp.MustCompile(
	`[a-z0-9][a-z0-9._-]*\.(?:jpg|jpeg|png|gif|webp|bmp|mp3|wav|ogg|m4a|flac|mp4|avi|mov|webm)(?:[^a-z0-9]|$)`,
)

// hasAttachments returns true when the message content contains embedded media.
// It checks for base64 data URIs (data:image/, data:audio/, data:video/) and
// common image/audio URL/file extensions. Fenced code blocks are ignored first
// for both checks, so a code snippet that merely mentions a media file name or
// embeds a sample data URI (e.g. an HTML <img> tag) does not force the heavy
// model. Real media arrives as a structured field (see SelectModelWithMedia),
// not as a fenced text snippet. This is intentionally conservative — false
// negatives (missing an attachment) just mean the routing falls back to the
// primary model anyway.
func hasAttachments(msg string) bool {
	lower := strings.ToLower(stripFencedCodeBlocks(msg))

	// Base64 data URIs embedded directly in the message
	if strings.Contains(lower, "data:image/") ||
		strings.Contains(lower, "data:audio/") ||
		strings.Contains(lower, "data:video/") {
		return true
	}

	// Common image/audio extensions in URLs or file references
	return mediaExtensionRegex.MatchString(lower)
}

// stripFencedCodeBlocks removes fenced code blocks (``` or ~~~) so that media
// file names mentioned in code are not mistaken for attachments. Like
// countCodeBlocks, a block is closed only by a fence of the same type that
// opened it, so a different fence inside a block is dropped with the rest of
// its content. An unclosed opening fence drops everything after it.
func stripFencedCodeBlocks(msg string) string {
	if !strings.Contains(msg, "```") && !strings.Contains(msg, "~~~") {
		return msg
	}

	var b strings.Builder
	b.Grow(len(msg))
	for rest := msg; ; {
		i, marker := nextFence(rest)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:i])
		rest = rest[i+len(marker):]
		end := strings.Index(rest, marker)
		if end < 0 {
			return b.String()
		}
		rest = rest[end+len(marker):]
	}
}
