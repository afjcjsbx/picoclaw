//go:build linux

package hardwaretools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestI2CWritePolicyGatesBeforeOpeningHardware(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	writeArgs := map[string]any{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x76),
		"data":    []any{float64(1), float64(2)},
		"confirm": true,
	}

	result := NewI2CTool().Execute(ctx, writeArgs)
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "writes are disabled") {
		t.Fatalf("read-only write = %#v, want a write-disabled error", result)
	}

	allowlisted := NewI2CTool(I2COptions{
		Policy: NewI2CWritePolicy(false, []string{"1:0x77"}, false, 30),
	})
	result = allowlisted.Execute(ctx, map[string]any{
		"action":  "read",
		"bus":     "1",
		"address": float64(0x76),
		"length":  float64(1),
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "not in") {
		t.Fatalf("unlisted read = %#v, want an allowlist error", result)
	}

	result = allowlisted.Execute(ctx, map[string]any{"action": "scan", "bus": "2"})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "not in") {
		t.Fatalf("unlisted scan = %#v, want an allowlist error", result)
	}
}

func TestI2CWriteDryRunDoesNotOpenHardware(t *testing.T) {
	t.Parallel()

	tool := NewI2CTool(I2COptions{
		Policy: NewI2CWritePolicy(false, nil, true, 30),
	})
	result := tool.Execute(context.Background(), map[string]any{
		"action":   "write",
		"bus":      "1",
		"address":  float64(0x76),
		"register": float64(0xf4),
		"data":     []any{float64(0x25)},
		"confirm":  true,
	})
	if result == nil || result.IsError {
		t.Fatalf("dry-run = %#v", result)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &decoded); err != nil {
		t.Fatalf("dry-run is not JSON: %v (%q)", err, result.ForLLM)
	}
	if decoded["dry_run"] != true || decoded["address"] != "0x76" {
		t.Fatalf("dry-run = %#v", decoded)
	}
}

func TestSPIWritePolicyGatesBeforeOpeningHardware(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	transferArgs := map[string]any{
		"action":  "transfer",
		"device":  "2.0",
		"data":    []any{float64(1), float64(2)},
		"confirm": true,
	}

	result := NewSPITool().Execute(ctx, transferArgs)
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "writes are disabled") {
		t.Fatalf("read-only transfer = %#v, want a write-disabled error", result)
	}

	dryRun := NewSPITool(SPIOptions{
		Policy: NewSPIWritePolicy(false, nil, true, 30),
	})
	result = dryRun.Execute(ctx, map[string]any{
		"action": "transfer",
		"device": "2.0",
		"segments": []any{
			map[string]any{"data": []any{float64(1), float64(2)}},
			map[string]any{"read_length": float64(4)},
		},
		"confirm": true,
	})
	if result == nil || result.IsError {
		t.Fatalf("dry-run = %#v", result)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &decoded); err != nil {
		t.Fatalf("dry-run is not JSON: %v (%q)", err, result.ForLLM)
	}
	if decoded["dry_run"] != true || decoded["device"] != "/dev/spidev2.0" {
		t.Fatalf("dry-run = %#v", decoded)
	}
}
