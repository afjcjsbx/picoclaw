package evolution

import "context"

// Message is the minimal chat message the evolution runtime needs. Keeping this
// local avoids compiling the full provider SDK stack into the optional plugin.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// LLM is the minimal chat interface used by the cold path. Implementations must
// be safe for concurrent use because the cold path may run per workspace.
type LLM interface {
	Chat(ctx context.Context, messages []Message, model string, temperature float64) (string, error)
	GetDefaultModel() string
}
