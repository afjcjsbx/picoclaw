package hardwaretools

import (
	"runtime"
	"strings"
	"testing"
)

func TestDescribeI2CWrite(t *testing.T) {
	t.Parallel()

	request, ok := DescribeWrite("i2c", map[string]any{
		"action":  "write",
		"bus":     "1",
		"address": float64(0x76),
		"data":    []any{float64(0xf4), float64(0x25)},
		"confirm": true,
	})
	if !ok {
		t.Fatal("DescribeWrite() rejected a valid i2c write")
	}
	if request.Target != "1:0x76" {
		t.Fatalf("target = %q, want 1:0x76", request.Target)
	}
	for _, want := range []string{"Tool: i2c", "bus 1", "0x76", "0xf4 0x25"} {
		if !strings.Contains(request.Summary, want) {
			t.Fatalf("summary = %q, missing %q", request.Summary, want)
		}
	}

	if _, ok := DescribeWrite("i2c", map[string]any{
		"action": "write", "bus": "1", "address": float64(0x76), "data": []any{float64(1)},
	}); ok {
		t.Fatal("DescribeWrite() accepted a write without confirm")
	}
	if _, ok := DescribeWrite("i2c", map[string]any{
		"action": "read", "bus": "1", "address": float64(0x76), "length": float64(1),
	}); ok {
		t.Fatal("DescribeWrite() accepted a read")
	}
}

func TestDescribeSPIWrite(t *testing.T) {
	t.Parallel()

	request, ok := DescribeWrite("spi", map[string]any{
		"action": "transfer",
		"device": "2.0",
		"segments": []any{
			map[string]any{"data": []any{float64(1), float64(2)}},
			map[string]any{"read_length": float64(4), "delay_usecs": float64(10)},
		},
		"confirm": true,
	})
	if !ok {
		t.Fatal("DescribeWrite() rejected a valid spi transfer")
	}
	if request.Target != "2.0" {
		t.Fatalf("target = %q, want 2.0", request.Target)
	}
	for _, want := range []string{"Tool: spi", "/dev/spidev2.0", "Segments: 2", "0x01 0x02", "read 4"} {
		if !strings.Contains(request.Summary, want) {
			t.Fatalf("summary = %q, missing %q", request.Summary, want)
		}
	}
}

func TestDescribeSerialWrite(t *testing.T) {
	t.Parallel()

	port := "/dev/ttyUSB0"
	target := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
		target = `\\.\COM3`
	}
	request, ok := DescribeWrite("serial", map[string]any{
		"action":  "write",
		"port":    port,
		"text":    "AT\r\n",
		"confirm": true,
	})
	if !ok {
		t.Fatal("DescribeWrite() rejected a valid serial write")
	}
	if request.Target != target {
		t.Fatalf("target = %q, want %q", request.Target, target)
	}
	for _, want := range []string{"Tool: serial", "115200", `"AT\r\n"`} {
		if !strings.Contains(request.Summary, want) {
			t.Fatalf("summary = %q, missing %q", request.Summary, want)
		}
	}
}

func TestTargetAllowed(t *testing.T) {
	t.Parallel()

	if !TargetAllowed("i2c", "1:0x76", nil) {
		t.Fatal("empty allowlist should allow the target")
	}
	if !TargetAllowed("i2c", "1:0x76", []string{"1:118"}) {
		t.Fatal("i2c allowlist did not normalize the address")
	}
	if TargetAllowed("i2c", "1:0x77", []string{"1:0x76"}) {
		t.Fatal("i2c allowlist allowed an unlisted address")
	}
	if !TargetAllowed("spi", "2.0", []string{" 2.0 "}) {
		t.Fatal("spi allowlist did not trim the device")
	}
	if TargetAllowed("spi", "2.1", []string{"2.0"}) {
		t.Fatal("spi allowlist allowed an unlisted device")
	}
}
