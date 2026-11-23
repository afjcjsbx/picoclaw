package hardwaretools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultSerialBaud      = 115200
	defaultSerialDataBits  = 8
	defaultSerialStopBits  = 1
	defaultSerialTimeoutMS = 1000
	maxSerialPayloadBytes  = 4096
	maxSerialReadBytes     = 4096
	serialPollInterval     = 100 * time.Millisecond
)

var (
	unixSerialBasePattern = regexp.MustCompile(
		`^(?:ttyS\d+|ttyUSB\d+|ttyACM\d+|ttyAMA\d+|serial\d+|rfcomm\d+|tty\.[A-Za-z0-9._-]+|cu\.[A-Za-z0-9._-]+)$`,
	)
	windowsSerialPortPattern = regexp.MustCompile(`^(?:\\\\\.\\)?COM[1-9]\d*$`)
)

type SerialTool struct{}

type serialPortInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type serialConfig struct {
	Port        string
	Baud        int
	DataBits    int
	Parity      string
	StopBits    int
	FlowControl string
}

func NewSerialTool() *SerialTool {
	return &SerialTool{}
}

func (t *SerialTool) Name() string {
	return "serial"
}

func (t *SerialTool) Description() string {
	return "Interact with host serial ports. Actions: list (enumerate ports), read (receive an exact byte count, bytes up to a delimiter, or currently available bytes), write (send bytes with explicit confirmation). Supports Linux, macOS, and Windows."
}

func (t *SerialTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "read", "write"},
				"description": "Action to perform: list available serial ports, read bytes from a port, or write bytes to a port.",
			},
			"port": map[string]any{
				"type":        "string",
				"description": "Serial port path or name, for example /dev/ttyUSB0, /dev/cu.usbserial-0001, /dev/serial/by-id/usb-..., or COM3. Required for read/write.",
			},
			"baud": map[string]any{
				"type":        "integer",
				"description": "Baud rate. Default: 115200. Linux supports standard termios rates up to 4000000; macOS supports standard rates plus arbitrary rates through IOSSIOSPEED; Windows accepts rates up to 4000000.",
			},
			"data_bits": map[string]any{
				"type":        "integer",
				"description": "Data bits. Supported values: 5, 6, 7, 8. Default: 8.",
			},
			"parity": map[string]any{
				"type":        "string",
				"enum":        []string{"none", "even", "odd"},
				"description": "Parity mode. Default: none.",
			},
			"stop_bits": map[string]any{
				"type":        "integer",
				"description": "Stop bits. Supported values: 1, 2. Default: 1.",
			},
			"flow_control": map[string]any{
				"type":        "string",
				"enum":        []string{"none", "rtscts", "xonxoff"},
				"description": "Flow control. none (default), rtscts (hardware RTS/CTS), or xonxoff (software XON/XOFF).",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Read/write timeout in milliseconds. Default: 1000.",
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Number of bytes to read with a plain read (1-4096). Required unless read_until or read_available is set; in those modes it caps the buffer. Default cap: 4096.",
			},
			"read_until": map[string]any{
				"type":        "string",
				"description": "Read until this delimiter, for example \"\\n\" for NMEA or \"\\r\\n\" for AT commands. The delimiter is included in the payload.",
			},
			"read_available": map[string]any{
				"type":        "boolean",
				"description": "Read whatever is available, stopping after 50 ms of idle time or when timeout_ms expires.",
			},
			"data": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Bytes to write, each in range 0-255. Required for write unless text is provided.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "UTF-8 text to write. Required for write if data is omitted.",
			},
			"confirm": map[string]any{
				"type":        "boolean",
				"description": "Must be true for write operations.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SerialTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, ok := args["action"].(string)
	if !ok || strings.TrimSpace(action) == "" {
		return ErrorResult("action is required")
	}

	switch action {
	case "list":
		return t.list()
	case "read":
		return t.read(ctx, args)
	case "write":
		return t.write(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (valid: list, read, write)", action))
	}
}

func (t *SerialTool) list() *ToolResult {
	ports, err := serialListPorts()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list serial ports: %v", err))
	}
	if len(ports) == 0 {
		return SilentResult("No serial ports found on this host.")
	}

	result, _ := json.MarshalIndent(map[string]any{
		"ports": ports,
		"count": len(ports),
	}, "", "  ")
	return SilentResult(string(result))
}

func (t *SerialTool) read(ctx context.Context, args map[string]any) *ToolResult {
	cfg, errResult := parseSerialConfig(args)
	if errResult != nil {
		return errResult
	}

	timeout, errResult := parseSerialTimeout(args)
	if errResult != nil {
		return errResult
	}
	delimiter, hasDelimiter, err := parseSerialReadUntil(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	readAvailable, err := parseSerialReadAvailable(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if hasDelimiter && readAvailable {
		return ErrorResult("read_until and read_available cannot be used together")
	}
	length, hasLength, err := parseSerialReadLength(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	if hasDelimiter || readAvailable {
		if !hasLength {
			length = maxSerialReadBytes
		}
		var data []byte
		var extra map[string]any
		switch {
		case hasDelimiter:
			data, err = serialReadUntil(ctx, cfg, delimiter, length, timeout)
			extra = map[string]any{
				"read_until": string(delimiter),
				"matched":    bytes.HasSuffix(data, delimiter),
			}
		case readAvailable:
			data, err = serialReadAvailable(ctx, cfg, length, timeout)
			extra = map[string]any{"read_available": true}
		}
		if err != nil {
			return ErrorResult(fmt.Sprintf("serial read failed on %s: %v", cfg.Port, err))
		}
		return SilentResult(formatSerialPayload("read", cfg, data, timeout, extra))
	}

	if !hasLength {
		return ErrorResult("length is required for read (1-4096) unless read_until or read_available is set")
	}
	data, err := serialRead(ctx, cfg, length, timeout)
	if err != nil {
		return ErrorResult(fmt.Sprintf("serial read failed on %s: %v", cfg.Port, err))
	}

	return SilentResult(formatSerialPayload("read", cfg, data, timeout, nil))
}

func (t *SerialTool) write(ctx context.Context, args map[string]any) *ToolResult {
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return ErrorResult(
			"write operations require confirm: true. Please confirm with the user before sending bytes to a serial device.",
		)
	}

	cfg, errResult := parseSerialConfig(args)
	if errResult != nil {
		return errResult
	}
	timeout, errResult := parseSerialTimeout(args)
	if errResult != nil {
		return errResult
	}
	payload, errResult := parseSerialWritePayload(args)
	if errResult != nil {
		return errResult
	}

	written, err := serialWrite(ctx, cfg, payload, timeout)
	if err != nil {
		return ErrorResult(fmt.Sprintf("serial write failed on %s: %v", cfg.Port, err))
	}

	return SilentResult(formatSerialPayload("write", cfg, payload, timeout, map[string]any{
		"written": written,
	}))
}

func parseSerialConfig(args map[string]any) (serialConfig, *ToolResult) {
	port, ok := args["port"].(string)
	port = strings.TrimSpace(port)
	if !ok || port == "" {
		return serialConfig{}, ErrorResult(
			"port is required (for example /dev/ttyUSB0, /dev/cu.usbserial-0001, or COM3)",
		)
	}

	normalizedPort, err := normalizeSerialPort(port)
	if err != nil {
		return serialConfig{}, ErrorResult(err.Error())
	}

	cfg := serialConfig{
		Port:        normalizedPort,
		Baud:        defaultSerialBaud,
		DataBits:    defaultSerialDataBits,
		Parity:      "none",
		StopBits:    defaultSerialStopBits,
		FlowControl: "none",
	}

	if v, ok := args["baud"].(float64); ok {
		cfg.Baud = int(v)
	}
	if err := validateSerialBaud(cfg.Baud); err != nil {
		return serialConfig{}, ErrorResult(err.Error())
	}

	if v, ok := args["data_bits"].(float64); ok {
		cfg.DataBits = int(v)
	}
	switch cfg.DataBits {
	case 5, 6, 7, 8:
	default:
		return serialConfig{}, ErrorResult("data_bits must be one of 5, 6, 7, or 8")
	}

	if v, ok := args["parity"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.Parity = strings.ToLower(strings.TrimSpace(v))
	}
	switch cfg.Parity {
	case "none", "even", "odd":
	default:
		return serialConfig{}, ErrorResult(`parity must be one of "none", "even", or "odd"`)
	}

	if v, ok := args["stop_bits"].(float64); ok {
		cfg.StopBits = int(v)
	}
	if cfg.StopBits != 1 && cfg.StopBits != 2 {
		return serialConfig{}, ErrorResult("stop_bits must be 1 or 2")
	}

	if v, ok := args["flow_control"].(string); ok && strings.TrimSpace(v) != "" {
		normalized := strings.ToLower(strings.TrimSpace(v))
		normalized = strings.NewReplacer("/", "", "-", "", "_", "").Replace(normalized)
		switch normalized {
		case "none":
			cfg.FlowControl = "none"
		case "rtscts", "hardware":
			cfg.FlowControl = "rtscts"
		case "xonxoff", "software":
			cfg.FlowControl = "xonxoff"
		default:
			return serialConfig{}, ErrorResult(`flow_control must be one of "none", "rtscts", or "xonxoff"`)
		}
	}

	return cfg, nil
}

func parseSerialTimeout(args map[string]any) (time.Duration, *ToolResult) {
	timeoutMS := defaultSerialTimeoutMS
	if v, ok := args["timeout_ms"].(float64); ok {
		timeoutMS = int(v)
	}
	if timeoutMS < 1 || timeoutMS > 60000 {
		return 0, ErrorResult("timeout_ms must be between 1 and 60000")
	}
	return time.Duration(timeoutMS) * time.Millisecond, nil
}

func parseSerialReadLength(args map[string]any) (int, bool, error) {
	raw, ok := args["length"]
	if !ok || raw == nil {
		return 0, false, nil
	}
	value, err := parseI2CScalar(raw, "length")
	if err != nil {
		return 0, false, err
	}
	if value < 1 || value > maxSerialReadBytes {
		return 0, false, fmt.Errorf("length must be between 1 and %d", maxSerialReadBytes)
	}
	return value, true, nil
}

func parseSerialReadUntil(args map[string]any) ([]byte, bool, error) {
	raw, ok := args["read_until"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return nil, false, fmt.Errorf("read_until must be a string")
	}
	delimiter, err := decodeSerialDelimiter(text)
	if err != nil {
		return nil, false, err
	}
	if len(delimiter) == 0 {
		return nil, false, fmt.Errorf("read_until must not be empty")
	}
	if len(delimiter) > 32 {
		return nil, false, fmt.Errorf("read_until must be at most 32 bytes")
	}
	return delimiter, true, nil
}

func parseSerialReadAvailable(args map[string]any) (bool, error) {
	raw, ok := args["read_available"]
	if !ok || raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("read_available must be a boolean")
	}
	return value, nil
}

// decodeSerialDelimiter turns the sequences an LLM writes in JSON into bytes.
// A literal newline is already a valid delimiter; this also accepts "\\n",
// "\\r\\n", "\\t", "\\\\", and "\\xNN".
func decodeSerialDelimiter(text string) ([]byte, error) {
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			out = append(out, text[i])
			continue
		}
		i++
		if i >= len(text) {
			return nil, fmt.Errorf("read_until ends with a trailing backslash")
		}
		switch text[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '\\':
			out = append(out, '\\')
		case 'x':
			if i+2 >= len(text) {
				return nil, fmt.Errorf("read_until has an incomplete \\x escape")
			}
			value, err := strconv.ParseUint(text[i+1:i+3], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("read_until has an invalid \\x escape")
			}
			out = append(out, byte(value))
			i += 2
		default:
			return nil, fmt.Errorf("read_until has an unsupported escape \\%c", text[i])
		}
	}
	return out, nil
}

func parseSerialWritePayload(args map[string]any) ([]byte, *ToolResult) {
	if text, ok := args["text"].(string); ok && text != "" {
		if !utf8.ValidString(text) {
			return nil, ErrorResult("text must be valid UTF-8")
		}
		if len(text) > maxSerialPayloadBytes {
			return nil, ErrorResult(fmt.Sprintf("text payload too large: maximum %d bytes", maxSerialPayloadBytes))
		}
		return []byte(text), nil
	}

	dataRaw, ok := args["data"].([]any)
	if !ok || len(dataRaw) == 0 {
		return nil, ErrorResult("write requires either text or data")
	}
	if len(dataRaw) > maxSerialPayloadBytes {
		return nil, ErrorResult(fmt.Sprintf("data too long: maximum %d bytes", maxSerialPayloadBytes))
	}

	data := make([]byte, len(dataRaw))
	for i, v := range dataRaw {
		f, ok := v.(float64)
		if !ok {
			return nil, ErrorResult(fmt.Sprintf("data[%d] is not a valid byte value", i))
		}
		if f != math.Trunc(f) {
			return nil, ErrorResult(fmt.Sprintf("data[%d] is not an integer byte value", i))
		}
		b := int(f)
		if b < 0 || b > 255 {
			return nil, ErrorResult(fmt.Sprintf("data[%d] = %d is out of byte range (0-255)", i, b))
		}
		data[i] = byte(b)
	}

	return data, nil
}

// formatSerialPayload renders the JSON result shared by the serial actions.
// extra carries the fields only some actions report, such as the byte count of
// a write; encoding/json sorts map keys, so the output is deterministic.
func formatSerialPayload(
	action string,
	cfg serialConfig,
	data []byte,
	timeout time.Duration,
	extra map[string]any,
) string {
	fields := map[string]any{
		"action":       action,
		"port":         cfg.Port,
		"baud":         cfg.Baud,
		"data_bits":    cfg.DataBits,
		"parity":       cfg.Parity,
		"stop_bits":    cfg.StopBits,
		"flow_control": cfg.FlowControl,
		"timeout_ms":   timeout.Milliseconds(),
		"payload":      serialPayloadSummary(data),
	}
	for key, value := range extra {
		fields[key] = value
	}

	result, _ := json.MarshalIndent(fields, "", "  ")
	return string(result)
}

func serialPayloadSummary(data []byte) map[string]any {
	hexValues := make([]string, len(data))
	intValues := make([]int, len(data))
	for i, b := range data {
		hexValues[i] = fmt.Sprintf("0x%02x", b)
		intValues[i] = int(b)
	}

	summary := map[string]any{
		"length": len(data),
		"bytes":  intValues,
		"hex":    hexValues,
	}
	if utf8.Valid(data) {
		summary["text"] = string(data)
	}
	return summary
}

func normalizeSerialPort(port string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		return normalizeWindowsSerialPath(port)
	case "linux", "darwin":
		return normalizeUnixSerialPath(port)
	default:
		if normalized, err := normalizeUnixSerialPath(port); err == nil {
			return normalized, nil
		}
		return normalizeWindowsSerialPath(port)
	}
}

func normalizeUnixSerialPath(port string) (string, error) {
	return normalizeUnixSerialPathAt(port, "/dev")
}

// normalizeUnixSerialPathAt resolves stable /dev/serial/by-id and by-path
// links to the real tty device. The resolved path must still be a serial
// device directly under devRoot, so a symlink cannot escape the allowlist.
func normalizeUnixSerialPathAt(port, devRoot string) (string, error) {
	trimmed := strings.TrimSpace(port)
	if trimmed == "" {
		return "", fmt.Errorf("serial port must not be empty")
	}
	if strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("invalid serial port: %q contains a path traversal segment", trimmed)
	}
	devRoot = filepath.Clean(devRoot)
	if resolvedRoot, err := filepath.EvalSymlinks(devRoot); err == nil {
		devRoot = resolvedRoot
	}

	byID := filepath.Join(devRoot, "serial", "by-id") + string(filepath.Separator)
	byPath := filepath.Join(devRoot, "serial", "by-path") + string(filepath.Separator)
	if strings.HasPrefix(trimmed, byID) || strings.HasPrefix(trimmed, byPath) {
		resolved, err := filepath.EvalSymlinks(trimmed)
		if err != nil {
			return "", fmt.Errorf(
				"invalid serial port: %q does not resolve to a device (is the adapter connected?): %w",
				trimmed,
				err,
			)
		}
		resolved = filepath.Clean(resolved)
		if filepath.Dir(resolved) != devRoot || !unixSerialBasePattern.MatchString(filepath.Base(resolved)) {
			return "", fmt.Errorf(
				"invalid serial port: %q resolves to %q, which is not a serial device",
				trimmed,
				resolved,
			)
		}
		return resolved, nil
	}

	switch {
	case strings.HasPrefix(trimmed, devRoot+string(filepath.Separator)):
		cleaned := filepath.Clean(trimmed)
		if filepath.Dir(cleaned) != devRoot || !unixSerialBasePattern.MatchString(filepath.Base(cleaned)) {
			return "", fmt.Errorf(
				"invalid serial port: expected a safe Unix device name such as /dev/ttyUSB0, /dev/cu.usbserial-0001, or /dev/serial/by-id/...",
			)
		}
		return cleaned, nil
	case filepath.IsAbs(trimmed):
		return "", fmt.Errorf(
			"invalid serial port: %q is outside %s",
			trimmed,
			devRoot,
		)
	default:
		if !unixSerialBasePattern.MatchString(trimmed) {
			return "", fmt.Errorf(
				"invalid serial port: expected a safe Unix device name such as /dev/ttyUSB0, /dev/cu.usbserial-0001, or /dev/serial/by-id/...",
			)
		}
		return filepath.Join(devRoot, trimmed), nil
	}
}

func normalizeWindowsSerialPath(port string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(port))
	if !windowsSerialPortPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid serial port: expected a COM port such as COM3")
	}
	if strings.HasPrefix(trimmed, `\\.\`) {
		return trimmed, nil
	}
	return `\\.\` + trimmed, nil
}

func validateSerialBaud(baud int) error {
	if baud < 50 || baud > 4000000 {
		return fmt.Errorf("baud must be between 50 and 4000000")
	}
	return validatePlatformSerialBaud(baud)
}

func serialContextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func serialWriteAll(
	ctx context.Context,
	data []byte,
	timeout time.Duration,
	now func() time.Time,
	write func([]byte) (int, error),
) (int, error) {
	if err := serialContextErr(ctx); err != nil {
		return 0, err
	}

	total := 0
	deadline := now().Add(timeout)
	for total < len(data) {
		if err := serialContextErr(ctx); err != nil {
			return total, err
		}
		if deadline.Sub(now()) <= 0 {
			return total, fmt.Errorf("timeout while writing serial data")
		}

		n, err := write(data[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			continue
		}
	}

	return total, nil
}
