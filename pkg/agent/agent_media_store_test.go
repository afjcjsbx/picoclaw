package agent

import (
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

// TestSetMediaStoreConcurrentWithReaders verifies that swapping the media store
// at runtime (gateway restart) is synchronized with agent paths that read it
// while turns are being processed.
func TestSetMediaStoreConcurrentWithReaders(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ModelName: "gpt-4o-mini",
				Provider:  "mock",
			},
		},
	}
	al := NewAgentLoop(cfg, nil, &simpleMockProviderAPI{response: "ok"})
	store := media.NewFileMediaStore()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				al.SetMediaStore(store)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = al.hasAudioInput(bus.InboundMessage{})
			}
		}()
	}
	wg.Wait()
}
