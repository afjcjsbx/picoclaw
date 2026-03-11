package voice

import (
	"context"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// Transcriber is the interface that all voice transcription backends must
// implement.
type Transcriber interface {
	Name() string
	Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error)
}

// TranscriptionResponse is the result returned by any Transcriber.
type TranscriptionResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// DetectTranscriber inspects cfg and returns the appropriate Transcriber, or
// nil if no supported transcription provider is configured.
//
// Resolution order:
//  1. If voice.transcriber contains a comma-separated list (e.g.
//     "elevenlabs,groq,openai"), build a FallbackTranscriber chain.
//  2. If voice.transcriber is a single name, use the matching factory.
//  3. Otherwise, auto-detect (try groq first, then others).
func DetectTranscriber(cfg *config.Config) Transcriber {
	voiceCfg := cfg.Voice

	raw := strings.TrimSpace(voiceCfg.Transcriber)
	if raw == "" {
		return autoDetect(voiceCfg, cfg)
	}

	names := splitNames(raw)

	if len(names) == 1 {
		return buildSingle(names[0], voiceCfg, cfg)
	}

	// Build a fallback chain.
	var chain []Transcriber
	for _, name := range names {
		t := buildForChain(name, voiceCfg, cfg)
		if t != nil {
			chain = append(chain, t)
		}
	}

	switch len(chain) {
	case 0:
		logger.WarnCF("voice", "No transcribers could be built from chain", map[string]any{"requested": names})
		return nil
	case 1:
		return chain[0]
	default:
		logger.InfoCF("voice", "Transcription fallback chain configured", map[string]any{
			"chain": NewFallbackTranscriber(chain).Name(),
		})
		return NewFallbackTranscriber(chain)
	}
}

// splitNames parses a comma-separated list of provider names.
func splitNames(raw string) []string {
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if n := strings.TrimSpace(p); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// buildSingle creates a single transcriber by name using the top-level voice config.
func buildSingle(name string, voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
	factory, ok := getTranscriberFactory(name)
	if !ok {
		logger.WarnCF("voice", "Unknown transcriber provider", map[string]any{
			"requested":  name,
			"registered": registeredTranscriberNames(),
		})
		return nil
	}
	t := factory(voiceCfg, cfg)
	if t == nil {
		logger.WarnCF("voice", "Transcriber factory returned nil (missing credentials?)", map[string]any{"provider": name})
	}
	return t
}

// buildForChain creates a transcriber for one link in the fallback chain.
// It merges per-provider overrides from voice.fallback.<name> over the
// top-level voice config values.
func buildForChain(name string, voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
	factory, ok := getTranscriberFactory(name)
	if !ok {
		logger.WarnCF("voice", "Unknown transcriber in chain, skipping", map[string]any{
			"provider":   name,
			"registered": registeredTranscriberNames(),
		})
		return nil
	}

	// Build a per-provider VoiceConfig with overrides.
	merged := voiceCfg
	if override, ok := voiceCfg.Fallback[name]; ok {
		if override.APIKey != "" {
			merged.APIKey = override.APIKey
		}
		if override.APIBase != "" {
			merged.APIBase = override.APIBase
		}
		if override.Model != "" {
			merged.TranscriberModel = override.Model
		}
	}

	t := factory(merged, cfg)
	if t == nil {
		logger.WarnCF("voice", "Transcriber factory returned nil in chain, skipping", map[string]any{"provider": name})
	}
	return t
}

// autoDetect tries groq first (legacy default), then all other registered
// factories. Used when voice.transcriber is empty.
func autoDetect(voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
	if factory, ok := getTranscriberFactory("groq"); ok {
		if t := factory(voiceCfg, cfg); t != nil {
			return t
		}
	}
	for name, factory := range registry {
		if name == "groq" {
			continue
		}
		if t := factory(voiceCfg, cfg); t != nil {
			return t
		}
	}
	return nil
}
