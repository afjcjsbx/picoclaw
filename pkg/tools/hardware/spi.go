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
type SPITool struct {
	policy *WritePolicy
}

type SPIOptions struct {
	Policy *WritePolicy
}

var readOnlySPIPolicy = NewSPIWritePolicy(false, nil, false, 0)

func NewSPITool(opts ...SPIOptions) *SPITool {
	tool := &SPITool{policy: readOnlySPIPolicy}
	if len(opts) > 0 && opts[0].Policy != nil {
		tool.policy = opts[0].Policy
	}
	return tool
}

func (t *SPITool) writePolicy() *WritePolicy {
	if t == nil || t.policy == nil {
		return readOnlySPIPolicy
	}
	return t.policy
}

func (t *SPITool) Name() string {
	return "spi"
}

func (t *SPITool) Description() string {
	return "Interact with SPI bus devices for high-speed peripheral communication. Actions: list (find SPI devices), transfer (one or more full-duplex segments with optional per-segment delay and CS change), read (receive bytes). Linux only."
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
				"description": "Bytes to send (0-255 each). Required for transfer unless segments is used.",
			},
			"read_length": map[string]any{
				"type":        "integer",
				"description": "For transfer: number of bytes to receive (0-4096). Omit for full-duplex (same length as data); 0 means write-only.",
			},
			"delay_usecs": map[string]any{
				"type":        "integer",
				"description": "Delay after this transfer in microseconds (0-65535). Useful between command and response phases.",
			},
			"cs_change": map[string]any{
				"type":        "boolean",
				"description": "Toggle chip select after this transfer, as defined by spidev.",
			},
			"segments": map[string]any{
				"type":        "array",
				"maxItems":    maxSPISegments,
				"description": "Multi-segment transfer in one ioctl, keeping CS asserted between segments. Each item supports data, read_length, delay_usecs, and cs_change. Conflicts with top-level data/delay_usecs/cs_change.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"data": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "integer"},
							"description": "Bytes to send in this segment.",
						},
						"read_length": map[string]any{
							"type":        "integer",
							"description": "Bytes to receive in this segment. Omit for full-duplex; 0 for write-only.",
						},
						"delay_usecs": map[string]any{
							"type":        "integer",
							"description": "Delay after this segment (0-65535 microseconds).",
						},
						"cs_change": map[string]any{
							"type":        "boolean",
							"description": "Toggle chip select after this segment.",
						},
					},
				},
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Number of bytes to read (1-4096). Required for read action.",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Deprecated alias for length in the read action; use length.",
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

func parseSPIIntArg(args map[string]any, key string) (int, bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, false, nil
	}
	value, err := parseI2CScalar(raw, key)
	if err != nil {
		return 0, false, err
	}
	return value, true, nil
}

func parseSPIReadCount(args map[string]any) (int, error) {
	length, hasLength, err := parseSPIIntArg(args, "length")
	if err != nil {
		return 0, err
	}
	count, hasCount, err := parseSPIIntArg(args, "count")
	if err != nil {
		return 0, err
	}
	if hasLength && hasCount && length != count {
		return 0, fmt.Errorf("length and count disagree (%d vs %d); use only one", length, count)
	}
	switch {
	case hasLength:
	case hasCount:
		length = count
	default:
		return 0, fmt.Errorf("length is required for read (1-%d)", maxSPITransferSize)
	}
	if length < 1 || length > maxSPITransferSize {
		return 0, fmt.Errorf("length must be between 1 and %d", maxSPITransferSize)
	}
	return length, nil
}

func parseSPIReadLength(args map[string]any) (int, bool, error) {
	raw, ok := args["read_length"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	value, err := parseI2CScalar(raw, "read_length")
	if err != nil {
		return 0, false, err
	}
	if value < 0 || value > maxSPITransferSize {
		return 0, false, fmt.Errorf("read_length must be between 0 and %d", maxSPITransferSize)
	}
	return value, true, nil
}

func parseSPIDelayUsecs(args map[string]any) (int, error) {
	raw, ok := args["delay_usecs"]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := parseI2CScalar(raw, "delay_usecs")
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 65535 {
		return 0, fmt.Errorf("delay_usecs must be between 0 and 65535")
	}
	return value, nil
}

func parseSPIChangeCS(args map[string]any) (bool, error) {
	raw, ok := args["cs_change"]
	if !ok || raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("cs_change must be a boolean")
	}
	return value, nil
}

func parseSPISegment(args map[string]any, field string) (spiSegment, error) {
	data, _, err := parseI2CData(args, "data")
	if err != nil {
		return spiSegment{}, fmt.Errorf("%s: %w", field, err)
	}
	readLength, hasReadLength, err := parseSPIReadLength(args)
	if err != nil {
		return spiSegment{}, fmt.Errorf("%s: %w", field, err)
	}
	if len(data) == 0 && (!hasReadLength || readLength == 0) {
		return spiSegment{}, fmt.Errorf("%s: data or a positive read_length is required", field)
	}
	if !hasReadLength {
		readLength = -1
	}
	delay, err := parseSPIDelayUsecs(args)
	if err != nil {
		return spiSegment{}, fmt.Errorf("%s: %w", field, err)
	}
	changeCS, err := parseSPIChangeCS(args)
	if err != nil {
		return spiSegment{}, fmt.Errorf("%s: %w", field, err)
	}
	return spiSegment{
		Data:       data,
		ReadLength: readLength,
		DelayUsecs: delay,
		CSChange:   changeCS,
	}, nil
}

func parseSPISegments(args map[string]any) ([]spiSegment, bool, error) {
	raw, ok := args["segments"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, true, fmt.Errorf("segments must be a non-empty array")
	}
	if len(items) > maxSPISegments {
		return nil, true, fmt.Errorf("too many segments: %d (max %d)", len(items), maxSPISegments)
	}
	for _, key := range []string{"data", "read_length", "delay_usecs", "cs_change"} {
		if _, present := args[key]; present {
			return nil, true, fmt.Errorf("segments cannot be combined with top-level %s", key)
		}
	}

	segments := make([]spiSegment, 0, len(items))
	for i, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("segments[%d] must be an object", i)
		}
		segment, err := parseSPISegment(object, fmt.Sprintf("segments[%d]", i))
		if err != nil {
			return nil, true, err
		}
		segments = append(segments, segment)
	}
	return segments, true, nil
}

func parseSPISingleSegment(args map[string]any) (spiSegment, bool, error) {
	data, hasData, err := parseI2CData(args, "data")
	if err != nil {
		return spiSegment{}, false, err
	}
	if !hasData || len(data) == 0 {
		return spiSegment{}, false, nil
	}
	segment, err := parseSPISegment(args, "transfer")
	if err != nil {
		return spiSegment{}, false, err
	}
	return segment, true, nil
}

var _ = parseSPIArgs
