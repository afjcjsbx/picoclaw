package voice

import (
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	RegisterTranscriber("openai", func(voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
		apiKey := voiceCfg.APIKey
		if apiKey == "" {
			// Fall back to provider-level OpenAI key.
			apiKey = cfg.Providers.OpenAI.APIKey
		}
		if apiKey == "" {
			return nil
		}
		apiBase := voiceCfg.APIBase
		if apiBase == "" {
			base := cfg.Providers.OpenAI.APIBase
			if base != "" {
				apiBase = base
			} else {
				apiBase = "https://api.openai.com/v1"
			}
		}
		model := voiceCfg.TranscriberModel
		if model == "" {
			model = "whisper-1"
		}
		return NewOpenAICompatTranscriber("openai", apiKey, apiBase, model)
	})
}
