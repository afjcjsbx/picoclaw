package hardwaretools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
)

// SPITool provides SPI bus interaction for high-speed peripheral communication.
type SPITool struct{}

func NewSPITool() *SPITool {
	return &SPITool{}
}

func (t *SPITool) Name() string {
	return "spi"
}

func (t *SPITool) Description() string {
	return "Interact with SPI bus devices for high-speed peripheral communication. Actions: list (find SPI devices), transfer (full-duplex send/receive), read (receive bytes). Linux only."
}

func (t *SPITool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "transfer", "read"},
				"description": "Action to perform: list (find available SPI devices), transfer (full-duplex send/receive), read (receive bytes by sending zeros)",
			},
			"device": map[string]any{
				"type":        "string",
				"description": "SPI device identifier (e.g. \"2.0\" for /dev/spidev2.0). Required for transfer/read.",
			},
			"speed": map[string]any{
				"type":        "integer",
				"description": "SPI clock speed in Hz. Default: 1000000 (1 MHz).",
			},
			"mode": map[string]any{
				"type":        "integer",
				"description": "SPI mode (0-3). Default: 0. Mode sets CPOL and CPHA: 0=0,0 1=0,1 2=1,0 3=1,1.",
			},
			"bits": map[string]any{
				"type":        "integer",
				"description": "Bits per word. Only 8 is supported; 9-32 bit words are rejected rather than silently mis-encoded. Default: 8.",
			},
			"data": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Bytes to send (0-255 each). Required for transfer action.",
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Number of bytes to read (1-4096). Required for read action.",
			},
			"confirm": map[string]any{
				"type":        "boolean",
				"description": "Must be true for transfer operations. Safety guard to prevent accidental writes.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SPITool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if runtime.GOOS != "linux" {
		return ErrorResult("SPI is only supported on Linux. This tool requires /dev/spidev* device files.")
	}

	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	switch action {
	case "list":
		return t.list()
	case "transfer":
		return t.transfer(args)
	case "read":
		return t.readDevice(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (valid: list, transfer, read)", action))
	}
}

// list finds available SPI devices by globbing /dev/spidev*
func (t *SPITool) list() *ToolResult {
	matches, err := filepath.Glob("/dev/spidev*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to scan for SPI devices: %v", err))
	}

	if len(matches) == 0 {
		return SilentResult(
			"No SPI devices found. You may need to:\n1. Enable SPI in device tree\n2. Configure pinmux for your board (see hardware skill)\n3. Check that spidev module is loaded",
		)
	}

	type devInfo struct {
		Path   string `json:"path"`
		Device string `json:"device"`
	}

	devices := make([]devInfo, 0, len(matches))
	re := regexp.MustCompile(`/dev/spidev(\d+\.\d+)`)
	for _, m := range matches {
		if sub := re.FindStringSubmatch(m); sub != nil {
			devices = append(devices, devInfo{Path: m, Device: sub[1]})
		}
	}

	result, _ := json.MarshalIndent(devices, "", "  ")
	return SilentResult(fmt.Sprintf("Found %d SPI device(s):\n%s", len(devices), string(result)))
}

// Helper function for SPI operations (used by platform-specific implementations)

// parseSPIArgs extracts and validates common SPI parameters
func parseSPIArgs(args map[string]any) (device string, speed uint32, mode uint8, bits uint8, errMsg string) {
	dev, ok := args["device"].(string)
	if !ok || dev == "" {
		return "", 0, 0, 0, "device is required (e.g. \"2.0\" for /dev/spidev2.0)"
	}
	matched, _ := regexp.MatchString(`^\d+\.\d+$`, dev)
	if !matched {
		return "", 0, 0, 0, "invalid device identifier: must be in format \"X.Y\" (e.g. \"2.0\")"
	}

	speed = 1000000 // default 1 MHz
	if raw, ok := args["speed"]; ok && raw != nil {
		value, err := parseI2CScalar(raw, "speed")
		if err != nil {
			return "", 0, 0, 0, err.Error()
		}
		if value < 1 || value > 125000000 {
			return "", 0, 0, 0, "speed must be between 1 Hz and 125 MHz"
		}
		speed = uint32(value)
	}

	mode = 0
	if raw, ok := args["mode"]; ok && raw != nil {
		value, err := parseI2CScalar(raw, "mode")
		if err != nil {
			return "", 0, 0, 0, err.Error()
		}
		if value < 0 || value > 3 {
			return "", 0, 0, 0, "mode must be 0-3"
		}
		mode = uint8(value)
	}

	bits = 8
	if raw, ok := args["bits"]; ok && raw != nil {
		value, err := parseI2CScalar(raw, "bits")
		if err != nil {
			return "", 0, 0, 0, err.Error()
		}
		if value != 8 {
			return "", 0, 0, 0, "bits other than 8 are not supported; the transfer buffer is byte-oriented"
		}
		bits = uint8(value)
	}

	return dev, speed, mode, bits, ""
}

var _ = parseSPIArgs
