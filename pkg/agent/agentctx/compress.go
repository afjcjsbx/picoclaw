package agentctx

import (
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	compressProtectedTail   = 4
	compressMinToolResultCh = 200
	prunedToolResult        = "[Old tool output cleared to save context space]"

	fallbackMinContentLength  = 200
	fallbackMaxContentPercent = 10
)

// PruneOldToolResults replaces large, media-free tool outputs outside the
// protected tail with a short placeholder. It mutates history in place and
// reports whether anything changed.
func PruneOldToolResults(history []providers.Message) bool {
	pruned := false
	for i := 0; i < len(history)-compressProtectedTail; i++ {
		if history[i].Role == "tool" && len(history[i].Content) > compressMinToolResultCh &&
			len(history[i].Media) == 0 && len(history[i].Attachments) == 0 {
			history[i].Content = prunedToolResult
			pruned = true
		}
	}
	return pruned
}

// EmergencyKeep returns the history tail kept by emergency compression: the
// newest half of Turns, or, when no safe split exists, only the latest user
// message (breaking Turn atomicity as a last resort).
func EmergencyKeep(history []providers.Message) []providers.Message {
	turns := ParseTurnBoundaries(history)
	var mid int
	if len(turns) >= 2 {
		mid = turns[len(turns)/2]
	} else {
		mid = FindSafeBoundary(history, len(history)/2)
	}
	if mid > 0 {
		return history[mid:]
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return []providers.Message{history[i]}
		}
	}
	return nil
}

// FindNearestUserMessage returns the user-message index closest to mid,
// searching backwards first, then forwards; mid if none exists.
func FindNearestUserMessage(messages []providers.Message, mid int) int {
	originalMid := mid

	for mid > 0 && messages[mid].Role != "user" {
		mid--
	}
	if messages[mid].Role == "user" {
		return mid
	}

	mid = originalMid
	for mid < len(messages) && messages[mid].Role != "user" {
		mid++
	}
	if mid < len(messages) {
		return mid
	}
	return originalMid
}

// FallbackSummary builds a truncated, LLM-free summary of batch.
func FallbackSummary(batch []providers.Message) string {
	var fallback strings.Builder
	fallback.WriteString("Conversation summary: ")
	for i, msg := range batch {
		if i > 0 {
			fallback.WriteString(" | ")
		}
		runes := []rune(strings.TrimSpace(msg.Content))
		if len(runes) == 0 {
			fmt.Fprintf(&fallback, "%s: ", msg.Role)
			continue
		}

		keepLength := min(max(len(runes)*fallbackMaxContentPercent/100, fallbackMinContentLength), len(runes))
		content := string(runes[:keepLength])
		if keepLength < len(runes) {
			content += "..."
		}
		fmt.Fprintf(&fallback, "%s: %s", msg.Role, content)
	}
	return fallback.String()
}

// EstimateMessagesTokens sums EstimateMessageTokens over messages.
func EstimateMessagesTokens(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}
