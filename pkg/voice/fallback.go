package voice

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// FallbackTranscriber tries a chain of transcribers in order.
// The first one that succeeds wins; if all fail the last error is returned.
type FallbackTranscriber struct {
	chain []Transcriber
}

// NewFallbackTranscriber creates a fallback chain. Panics if chain is empty.
func NewFallbackTranscriber(chain []Transcriber) *FallbackTranscriber {
	if len(chain) == 0 {
		panic("voice: FallbackTranscriber requires at least one transcriber")
	}
	return &FallbackTranscriber{chain: chain}
}

func (ft *FallbackTranscriber) Name() string {
	names := make([]string, len(ft.chain))
	for i, t := range ft.chain {
		names[i] = t.Name()
	}
	return strings.Join(names, "->")
}

func (ft *FallbackTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error) {
	var lastErr error
	for i, t := range ft.chain {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := t.Transcribe(ctx, audioFilePath)
		if err == nil {
			if i > 0 {
				logger.InfoCF("voice", "Transcription succeeded on fallback", map[string]any{
					"provider": t.Name(),
					"attempt":  i + 1,
				})
			}
			return resp, nil
		}

		lastErr = err
		logger.WarnCF("voice", "Transcriber failed, trying next", map[string]any{
			"provider":  t.Name(),
			"attempt":   i + 1,
			"remaining": len(ft.chain) - i - 1,
			"error":     err.Error(),
		})
	}

	return nil, fmt.Errorf("all %d transcribers failed, last error: %w", len(ft.chain), lastErr)
}
