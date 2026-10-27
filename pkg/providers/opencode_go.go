// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package providers

import (
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	anthropicmessages "github.com/sipeed/picoclaw/pkg/providers/anthropic_messages"
	"github.com/sipeed/picoclaw/pkg/providers/httpapi"
	openairesponses "github.com/sipeed/picoclaw/pkg/providers/openai_responses"
)

// OpenCode Go (https://opencode.ai/docs/go/) serves every model through one
// API key but on one of four wire protocols, each under its own path below the
// api_base (default https://opencode.ai/inference/go). Docs ask clients to send
// a stable per-conversation id in this header for routing and prompt caching,
// and the API answers 400 MissingSessionID without it.
const opencodeGoSessionHeader = "x-opencode-session"

type opencodeGoFamily int

const (
	opencodeGoChat      opencodeGoFamily = iota // {base}/openai/v1/chat/completions
	opencodeGoResponses                         // {base}/openai/v1/responses
	opencodeGoMessages                          // {base}/anthropic/v1/messages
	opencodeGoGoogle                            // {base}/google/v1beta/models/{id}:generateContent
)

// opencodeGoRoute picks the wire protocol and the path below api_base for a model.
//
// ponytail: routing is by vendor prefix (the docs publish no machine-readable
// family per model), so a new vendor on a non-chat endpoint needs one line here.
// Meanwhile `provider: openai` / `anthropic-messages` with the full family URL
// as api_base is the manual escape hatch.
func opencodeGoRoute(modelID string) (opencodeGoFamily, string) {
	m := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case hasAnyPrefix(m, "grok-", "gpt-", "muse-spark-"):
		return opencodeGoResponses, "/openai/v1"
	case hasAnyPrefix(m, "claude-", "minimax-", "qwen"):
		return opencodeGoMessages, "/anthropic/v1"
	case strings.HasPrefix(m, "gemini-"):
		return opencodeGoGoogle, "/google/v1beta"
	default:
		return opencodeGoChat, "/openai/v1"
	}
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func newOpenCodeGoProvider(cfg *config.ModelConfig, modelID, userAgent string) (LLMProvider, error) {
	if cfg.APIKey() == "" && cfg.APIBase == "" {
		return nil, fmt.Errorf("api_key is required for opencode-go protocol (model: %s)", cfg.Model)
	}

	family, path := opencodeGoRoute(modelID)
	apiBase := ResolveAPIBase(cfg) + path
	// Calls outside a conversation (summaries, subagents, ...) carry no
	// session_key but must still send the header; group them per model config.
	fallbackSession := "picoclaw-" + cfg.ModelName

	switch family {
	case opencodeGoResponses:
		p := openairesponses.NewProvider(
			cfg.APIKey(), apiBase, cfg.Proxy, userAgent, cfg.RequestTimeout, cfg.CustomHeaders,
		)
		p.SetSessionHeader(opencodeGoSessionHeader, fallbackSession)
		return p, nil
	case opencodeGoMessages:
		return anthropicmessages.NewProviderWithOptions(
			cfg.APIKey(), apiBase, userAgent, cfg.RequestTimeout,
			anthropicmessages.WithProxy(cfg.Proxy),
			anthropicmessages.WithCustomHeaders(cfg.CustomHeaders),
			anthropicmessages.WithSessionHeader(opencodeGoSessionHeader, fallbackSession),
		), nil
	case opencodeGoGoogle:
		p := httpapi.NewGeminiProvider(
			cfg.APIKey(), apiBase, cfg.Proxy, userAgent, cfg.RequestTimeout, cfg.ExtraBody, cfg.CustomHeaders,
		)
		p.SetSessionHeader(opencodeGoSessionHeader, fallbackSession)
		return p, nil
	default:
		p := httpapi.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(), apiBase, cfg.Proxy, cfg.MaxTokensField, userAgent, cfg.RequestTimeout,
			cfg.ExtraBody, cfg.CustomHeaders,
		)
		p.SetProviderName("opencode-go")
		p.SetSessionHeader(opencodeGoSessionHeader, fallbackSession)
		return p, nil
	}
}
