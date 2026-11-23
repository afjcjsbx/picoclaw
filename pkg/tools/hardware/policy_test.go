package hardwaretools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWritePolicyReadOnlyAndAllowlist(t *testing.T) {
	t.Parallel()

	policy := NewI2CWritePolicy(
		false,
		[]string{"1:0x76", "1:0x77", "not-a-device"},
		false,
		30,
	)
	if len(policy.Allow) != 2 {
		t.Fatalf("normalized allowlist = %#v, want two entries", policy.Allow)
	}
	if result := policy.checkTarget("1:0x76"); result != nil {
		t.Fatalf("checkTarget(1:0x76) = %v", result.ForLLM)
	}
	if result := policy.checkTarget("1:0x38"); result == nil {
		t.Fatal("checkTarget(1:0x38) was allowed")
	}
	if result := policy.checkTarget("1:0x76"); result != nil {
		t.Fatalf("checkTarget(1:0x76) = %v", result.ForLLM)
	}
	if result := policy.checkBus("1", "1:"); result != nil {
		t.Fatalf("checkBus(1) = %v", result.ForLLM)
	}
	if result := policy.checkBus("2", "2:"); result == nil {
		t.Fatal("checkBus(2) was allowed")
	}
	if result := policy.checkWrite("1:0x76", time.Now()); result == nil {
		t.Fatal("read-only policy allowed a write")
	} else if !strings.Contains(result.ForLLM, "writes are disabled") {
		t.Fatalf("write denial = %q", result.ForLLM)
	}
}

func TestWritePolicyRateLimit(t *testing.T) {
	t.Parallel()

	policy := NewWritePolicy("spi", true, nil, false, 2)
	now := time.Now()
	if result := policy.checkWrite("2.0", now); result != nil {
		t.Fatalf("first write denied: %s", result.ForLLM)
	}
	if result := policy.checkWrite("2.0", now); result != nil {
		t.Fatalf("second write denied: %s", result.ForLLM)
	}
	result := policy.checkWrite("2.0", now)
	if result == nil || !strings.Contains(result.ForLLM, "rate limit") {
		t.Fatalf("third write = %#v, want a rate limit", result)
	}
	if result := policy.checkWrite("2.0", now.Add(61*time.Second)); result != nil {
		t.Fatalf("write after the window was denied: %s", result.ForLLM)
	}
}

func TestWritePolicyUnlimitedRate(t *testing.T) {
	t.Parallel()

	policy := NewWritePolicy("spi", true, nil, false, -1)
	now := time.Now()
	for i := 0; i < 100; i++ {
		if result := policy.checkWrite("2.0", now); result != nil {
			t.Fatalf("write %d denied: %s", i, result.ForLLM)
		}
	}
}

func TestNewSPIWritePolicyNormalizesDevices(t *testing.T) {
	t.Parallel()

	policy := NewSPIWritePolicy(false, []string{"2.0", "bad", " 3.1 "}, false, 30)
	if len(policy.Allow) != 2 || policy.Allow[0] != "2.0" || policy.Allow[1] != "3.1" {
		t.Fatalf("allowlist = %#v", policy.Allow)
	}
}

func TestSerialWriteIsReadOnlyByDefault(t *testing.T) {
	t.Parallel()

	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}
	result := NewSerialTool().Execute(context.Background(), map[string]any{
		"action":  "write",
		"port":    port,
		"text":    "AT",
		"confirm": true,
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "writes are disabled") {
		t.Fatalf("write result = %#v, want a write-disabled error", result)
	}
}

func TestSerialDryRunDoesNotRequireWritePermission(t *testing.T) {
	t.Parallel()

	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(false, []string{port}, true, 30),
	})
	result := tool.Execute(context.Background(), map[string]any{
		"action":  "write",
		"port":    port,
		"text":    "AT",
		"confirm": true,
	})
	if result == nil || result.IsError {
		t.Fatalf("dry-run result = %#v", result)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &decoded); err != nil {
		t.Fatalf("dry-run result is not JSON: %v (%q)", err, result.ForLLM)
	}
	if decoded["dry_run"] != true {
		t.Fatalf("dry-run result = %#v", decoded)
	}
	payload, _ := decoded["payload"].(map[string]any)
	if payload["text"] != "AT" {
		t.Fatalf("dry-run payload = %#v", payload)
	}
}

func TestSerialAllowlistRejectsUnlistedWriteTarget(t *testing.T) {
	t.Parallel()

	port := "/dev/ttyUSB0"
	allowed := "/dev/ttyUSB1"
	if runtime.GOOS == "windows" {
		port = "COM3"
		allowed = "COM4"
	}
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(true, []string{allowed}, false, 30),
	})
	result := tool.Execute(context.Background(), map[string]any{
		"action":  "write",
		"port":    port,
		"text":    "AT",
		"confirm": true,
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "not in") {
		t.Fatalf("write result = %#v, want an allowlist error", result)
	}
}
