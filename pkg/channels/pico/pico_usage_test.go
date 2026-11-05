package pico

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestSetTurnUsagePayload(t *testing.T) {
	t.Run("populates usage block when counts present", func(t *testing.T) {
		payload := map[string]any{PayloadKeyContent: "hi"}
		setTurnUsagePayload(payload, 1234, 567)

		raw, ok := payload[PayloadKeyUsage]
		if !ok {
			t.Fatalf("expected %q key in payload", PayloadKeyUsage)
		}
		usage, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("usage block is not a map: %T", raw)
		}
		if usage["input_tokens"] != 1234 {
			t.Errorf("input_tokens = %v, want 1234", usage["input_tokens"])
		}
		if usage["output_tokens"] != 567 {
			t.Errorf("output_tokens = %v, want 567", usage["output_tokens"])
		}
		if usage["total_tokens"] != 1801 {
			t.Errorf("total_tokens = %v, want 1801", usage["total_tokens"])
		}
	})

	t.Run("omits usage block when both counts zero", func(t *testing.T) {
		payload := map[string]any{PayloadKeyContent: "hi"}
		setTurnUsagePayload(payload, 0, 0)
		if _, ok := payload[PayloadKeyUsage]; ok {
			t.Errorf("expected no %q key when counts are zero", PayloadKeyUsage)
		}
	})
}

func TestSetTokenUsagePayload(t *testing.T) {
	t.Run("includes cache counters", func(t *testing.T) {
		payload := map[string]any{PayloadKeyContent: "hi"}
		setTokenUsagePayload(payload, &bus.TokenUsage{
			InputTokens:         100,
			OutputTokens:        50,
			TotalTokens:         150,
			CacheReadTokens:     40,
			CacheCreationTokens: 10,
			CostUSD:             0.0123,
		})

		raw, ok := payload[PayloadKeyUsage].(map[string]any)
		if !ok {
			t.Fatalf("usage block is not a map: %T", payload[PayloadKeyUsage])
		}
		if raw["cache_read_tokens"] != 40 {
			t.Errorf("cache_read_tokens = %v, want 40", raw["cache_read_tokens"])
		}
		if raw["cache_creation_tokens"] != 10 {
			t.Errorf("cache_creation_tokens = %v, want 10", raw["cache_creation_tokens"])
		}
		if raw["cost_usd"] != 0.0123 {
			t.Errorf("cost_usd = %v, want 0.0123", raw["cost_usd"])
		}
	})

	t.Run("omits nil usage", func(t *testing.T) {
		payload := map[string]any{PayloadKeyContent: "hi"}
		setTokenUsagePayload(payload, nil)
		if _, ok := payload[PayloadKeyUsage]; ok {
			t.Errorf("expected no %q key for nil usage", PayloadKeyUsage)
		}
	})
}

// newCaptureStreamer returns a streamer whose broadcasts are captured into the
// returned map pointer, so tests need no live websocket.
func newCaptureStreamer() (*picoStreamer, *map[string]any) {
	var last map[string]any
	ch := &PicoChannel{}
	ch.broadcastFn = func(chatID string, msg PicoMessage) error {
		last = msg.Payload
		return nil
	}
	s := &picoStreamer{channel: ch, chatID: "c1"}
	return s, &last
}

func TestStreamerEmitsUsageOnFinalize(t *testing.T) {
	s, last := newCaptureStreamer()
	s.SetTurnUsage(100, 40)

	// sendLocked with empty messageID takes the create branch, which attaches
	// usage from the streamer's stored counts.
	s.mu.Lock()
	err := s.sendLocked(context.Background(), "answer", nil, false)
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("sendLocked: %v", err)
	}
	if _, ok := (*last)[PayloadKeyUsage]; !ok {
		t.Fatalf("expected usage in payload, got %+v", *last)
	}
}
