//go:build linux

package hardwaretools

import (
	"context"
	"strings"
	"testing"
)

func TestI2CScanHonorsCanceledContextBeforeTouchingHardware(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := NewI2CTool().Execute(ctx, map[string]any{
		"action": "scan",
		"bus":    "1",
	})
	if result == nil || !result.IsError {
		t.Fatalf("scan result = %#v, want error", result)
	}
	if !strings.Contains(result.ForLLM, "context canceled") {
		t.Fatalf("scan result = %q, want context canceled", result.ForLLM)
	}
}
