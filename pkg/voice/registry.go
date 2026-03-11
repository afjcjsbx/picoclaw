package voice

import (
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TranscriberFactory creates a Transcriber from a VoiceConfig and the full
// application config (so it can fall back to provider keys, model list, etc.).
// It returns nil if the factory cannot build a transcriber from the given config
// (e.g. missing API key).
type TranscriberFactory func(voiceCfg config.VoiceConfig, cfg *config.Config) Transcriber

var (
	registryMu sync.RWMutex
	registry   = map[string]TranscriberFactory{}
)

// RegisterTranscriber registers a named transcriber factory.
// Called from subpackage or same-package init() functions.
func RegisterTranscriber(name string, f TranscriberFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = f
}

// getTranscriberFactory looks up a transcriber factory by name.
func getTranscriberFactory(name string) (TranscriberFactory, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// registeredTranscriberNames returns all registered transcriber names (for logging).
func registeredTranscriberNames() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
