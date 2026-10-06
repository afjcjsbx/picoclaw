package loop

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

type voiceTestTTSProvider struct{}

func (voiceTestTTSProvider) Name() string { return "test-tts" }

func (voiceTestTTSProvider) Synthesize(_ context.Context, text string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(text)), nil
}

func TestVoiceModeSendsAudioForFinalChatResponse(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		withAudio bool
		wantAudio bool
	}{
		{name: "tts text", mode: "tts", wantAudio: true},
		{name: "on text", mode: "on"},
		{name: "on audio", mode: "on", withAudio: true, wantAudio: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
				Workspace: t.TempDir(), ModelName: "test-model", MaxTokens: 4096, MaxToolIterations: 10,
			}}}
			msgBus := bus.NewMessageBus()
			al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "spoken reply"})
			store := media.NewFileMediaStore()
			al.SetMediaStore(store)
			al.mu.Lock()
			al.ttsProvider = voiceTestTTSProvider{}
			al.mu.Unlock()

			msg := testInboundMessage(bus.InboundMessage{
				Channel: "telegram", ChatID: "chat-1", SenderID: "user-1", Content: "hello",
			})
			if tc.withAudio {
				path := filepath.Join(t.TempDir(), "voice.ogg")
				if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
					t.Fatal(err)
				}
				ref, err := store.Store(path, media.MediaMeta{
					Filename: "voice.ogg", ContentType: "audio/ogg",
				}, "incoming")
				if err != nil {
					t.Fatal(err)
				}
				msg.Media = []string{ref}
			}
			route, _, err := al.resolveMessageRoute(msg)
			if err != nil {
				t.Fatal(err)
			}
			sessionKey := resolveScopeKey(al.allocateRouteSession(route, msg).SessionKey, msg.SessionKey)
			if err := al.setVoiceMode(sessionKey, tc.mode); err != nil {
				t.Fatal(err)
			}

			al.runTurnWithSteering(context.Background(), msg)

			select {
			case got := <-msgBus.OutboundChan():
				if got.Content != "spoken reply" {
					t.Fatalf("outbound content = %q", got.Content)
				}
			default:
				t.Fatal("missing final text response")
			}
			select {
			case got := <-msgBus.OutboundMediaChan():
				if !tc.wantAudio {
					t.Fatalf("unexpected voice response: %+v", got)
				}
				if len(got.Parts) != 1 {
					t.Fatalf("voice response parts = %d", len(got.Parts))
				}
				path, _, err := store.ResolveWithMeta(got.Parts[0].Ref)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Remove(path) })
			default:
				if tc.wantAudio {
					t.Fatal("missing voice response")
				}
			}
		})
	}
}

func TestVoiceModeConcurrentWithProviderReload(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			_ = al.setVoiceMode("session", "tts")
		}
	}()
	for range 5 {
		if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfg); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}
