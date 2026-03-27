package embedding

import "fmt"

// AutoSelect probes providers in order and returns the first available one.
// Order: openai, gemini, voyage, mistral.
// Ollama is NOT auto-selected.
// Availability is determined by checking whether an API key is configured.
func AutoSelect(cfg Config) (Provider, error) {
	type candidate struct {
		name    string
		factory func(Config) Provider
	}

	candidates := []candidate{
		{"openai", func(c Config) Provider { return NewOpenAI(c) }},
		{"gemini", func(c Config) Provider { return NewGemini(c) }},
		{"voyage", func(c Config) Provider { return NewVoyage(c) }},
		{"mistral", func(c Config) Provider { return NewMistral(c) }},
	}

	for _, c := range candidates {
		if cfg.APIKey != "" {
			return c.factory(cfg), nil
		}
	}

	return nil, fmt.Errorf("embedding: no provider available — set an API key for one of: openai, gemini, voyage, mistral")
}
