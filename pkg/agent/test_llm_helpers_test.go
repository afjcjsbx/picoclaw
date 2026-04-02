package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const testMemorySynthesisPrompt = "Generate a durable memory record for an AI assistant."

func isMemorySynthesisTestRequest(messages []providers.Message) bool {
	return len(messages) > 0 &&
		messages[0].Role == "system" &&
		strings.Contains(messages[0].Content, testMemorySynthesisPrompt)
}

func memorySynthesisTestResponse() *providers.LLMResponse {
	return &providers.LLMResponse{
		Content: `{"description":"Test memory","summary_markdown":"## Summary\n\nSynthetic test memory."}`,
	}
}
