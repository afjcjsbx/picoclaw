package voice

import (
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	RegisterTranscriber("elevenlabs", func(voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber {
		apiKey := voiceCfg.APIKey
		if apiKey == "" {
			return nil
		}
		apiBase := voiceCfg.APIBase
		if apiBase == "" {
			apiBase = "https://api.elevenlabs.io/v1"
		}
		model := voiceCfg.TranscriberModel
		if model == "" {
			model = "scribe_v1"
		}
		return NewOpenAICompatTranscriber("elevenlabs", apiKey, apiBase, model)
	})
}
