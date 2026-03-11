package voice

import (
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	RegisterTranscriber("groq", func(voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
		apiKey, apiBase := resolveGroqCredentials(voiceCfg, cfg)
		if apiKey == "" {
			return nil
		}
		model := voiceCfg.TranscriberModel
		if model == "" {
			model = "whisper-large-v3"
		}
		return NewOpenAICompatTranscriber("groq", apiKey, apiBase, model)
	})
}

// resolveGroqCredentials extracts the Groq API key and base URL, checking
// (in priority order): voice config, direct provider config, model list.
func resolveGroqCredentials(voiceCfg config.VoiceConfig, cfg *config.Config) (apiKey, apiBase string) {
	// 1. Explicit voice-level credentials.
	if voiceCfg.APIKey != "" {
		return voiceCfg.APIKey, cond(voiceCfg.APIBase != "", voiceCfg.APIBase, "https://api.groq.com/openai/v1")
	}
	// 2. Direct Groq provider config.
	if key := cfg.Providers.Groq.APIKey; key != "" {
		base := cfg.Providers.Groq.APIBase
		if base == "" {
			base = "https://api.groq.com/openai/v1"
		}
		return key, base
	}
	// 3. Model list fallback: first groq/ entry with a key.
	for _, mc := range cfg.ModelList {
		if len(mc.Model) > 5 && mc.Model[:5] == "groq/" && mc.APIKey != "" {
			base := mc.APIBase
			if base == "" {
				base = "https://api.groq.com/openai/v1"
			}
			return mc.APIKey, base
		}
	}
	return "", ""
}

func cond(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}
