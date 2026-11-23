package hardwaretools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseSerialConfig(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	cfg, errResult := parseSerialConfig(map[string]any{
		"port":      port,
		"baud":      float64(9600),
		"data_bits": float64(7),
		"parity":    "even",
		"stop_bits": float64(2),
	})
	if errResult != nil {
		t.Fatalf("parseSerialConfig() unexpected error = %v", errResult.ForLLM)
	}

	wantPort := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		wantPort = `\\.\COM3`
	}
	if cfg.Port != wantPort || cfg.Baud != 9600 || cfg.DataBits != 7 || cfg.Parity != "even" || cfg.StopBits != 2 {
		t.Fatalf("parseSerialConfig() = %#v", cfg)
	}
	if cfg.FlowControl != "none" {
		t.Fatalf("parseSerialConfig() flow control = %q, want none", cfg.FlowControl)
	}
}

func TestParseSerialFlowControl(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	tests := []struct {
		input string
		want  string
	}{
		{input: "none", want: "none"},
		{input: "rtscts", want: "rtscts"},
		{input: "RTS/CTS", want: "rtscts"},
		{input: "hardware", want: "rtscts"},
		{input: "xonxoff", want: "xonxoff"},
		{input: "XON/XOFF", want: "xonxoff"},
		{input: "software", want: "xonxoff"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cfg, errResult := parseSerialConfig(map[string]any{
				"port":         port,
				"flow_control": tt.input,
			})
			if errResult != nil {
				t.Fatalf("parseSerialConfig() error = %v", errResult.ForLLM)
			}
			if cfg.FlowControl != tt.want {
				t.Fatalf("flow_control = %q, want %q", cfg.FlowControl, tt.want)
			}
		})
	}

	if _, errResult := parseSerialConfig(map[string]any{
		"port":         port,
		"flow_control": "dsr",
	}); errResult == nil {
		t.Fatal("parseSerialConfig() accepted an invalid flow_control")
	}
}

func TestParseSerialConfigRejectsInvalidParity(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	_, errResult := parseSerialConfig(map[string]any{
		"port":   port,
		"parity": "mark",
	})
	if errResult == nil {
		t.Fatal("expected invalid parity to fail")
	}
}

func TestParseSerialConfigRejectsUnsupportedLinuxBaud(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("only Linux has a fixed standard termios rate table")
	}

	_, errResult := parseSerialConfig(map[string]any{
		"port": "/dev/ttyUSB0",
		"baud": float64(123456),
	})
	if errResult == nil {
		t.Fatal("expected unsupported Linux baud rate to fail")
	}
}

func TestParseSerialConfigAcceptsHighBaud(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}
	for _, baud := range []int{460800, 921600} {
		cfg, errResult := parseSerialConfig(map[string]any{
			"port": port,
			"baud": float64(baud),
		})
		if errResult != nil {
			t.Fatalf("parseSerialConfig(%d) error = %v", baud, errResult.ForLLM)
		}
		if cfg.Baud != baud {
			t.Fatalf("parseSerialConfig(%d) baud = %d", baud, cfg.Baud)
		}
	}
}

func TestParseSerialRS485(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	if _, errResult := parseSerialConfig(map[string]any{
		"port":                  port,
		"rs485":                 true,
		"rs485_delay_before_ms": float64(10),
		"rs485_delay_after_ms":  float64(20),
	}); errResult == nil && runtime.GOOS != "linux" {
		t.Fatal("parseSerialConfig() accepted RS-485 on a non-Linux platform")
	}

	if _, errResult := parseSerialConfig(map[string]any{
		"port":                  port,
		"rs485_delay_before_ms": float64(10),
	}); errResult == nil {
		t.Fatal("parseSerialConfig() accepted an RS-485 delay without rs485=true")
	}

	if runtime.GOOS == "linux" {
		cfg, errResult := parseSerialConfig(map[string]any{
			"port":                  port,
			"rs485":                 true,
			"rs485_delay_before_ms": float64(10),
			"rs485_delay_after_ms":  float64(20),
		})
		if errResult != nil {
			t.Fatalf("parseSerialConfig() error = %v", errResult.ForLLM)
		}
		if !cfg.RS485 || cfg.RS485DelayBeforeMS != 10 || cfg.RS485DelayAfterMS != 20 {
			t.Fatalf("parseSerialConfig() = %#v", cfg)
		}
		if _, errResult := parseSerialConfig(map[string]any{
			"port":                  port,
			"rs485":                 true,
			"rs485_delay_before_ms": float64(1001),
		}); errResult == nil {
			t.Fatal("parseSerialConfig() accepted an RS-485 delay above 1000 ms")
		}
	}
}

func TestDecodeSerialDelimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "\\n", want: "\n"},
		{input: "\\r\\n", want: "\r\n"},
		{input: "\n", want: "\n"},
		{input: "END", want: "END"},
		{input: "\\x03", want: "\x03"},
		{input: "\\t", want: "\t"},
		{input: "\\\\", want: "\\"},
		{input: "\\", wantErr: true},
		{input: "\\q", wantErr: true},
		{input: "\\xZZ", wantErr: true},
		{input: "\\x1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := decodeSerialDelimiter(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("decodeSerialDelimiter(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeSerialDelimiter(%q) error = %v", tt.input, err)
			}
			if string(got) != tt.want {
				t.Fatalf("decodeSerialDelimiter(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSerialReadModeValidation(t *testing.T) {
	t.Parallel()

	if _, _, err := parseSerialReadUntil(map[string]any{"read_until": 123}); err == nil {
		t.Fatal("parseSerialReadUntil() accepted a non-string")
	}
	if _, _, err := parseSerialReadUntil(map[string]any{"read_until": ""}); err == nil {
		t.Fatal("parseSerialReadUntil() accepted an empty delimiter")
	}
	if _, _, err := parseSerialReadUntil(map[string]any{"read_until": strings.Repeat("x", 33)}); err == nil {
		t.Fatal("parseSerialReadUntil() accepted a delimiter longer than 32 bytes")
	}
	if _, err := parseSerialReadAvailable(map[string]any{"read_available": "yes"}); err == nil {
		t.Fatal("parseSerialReadAvailable() accepted a string")
	}
	if _, _, err := parseSerialReadLength(map[string]any{"length": float64(0)}); err == nil {
		t.Fatal("parseSerialReadLength() accepted zero")
	}
	if _, _, err := parseSerialReadLength(map[string]any{"length": float64(4097)}); err == nil {
		t.Fatal("parseSerialReadLength() accepted more than 4096 bytes")
	}
}

func TestSerialReadRequiresLengthUnlessReadModeIsSet(t *testing.T) {
	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	result := NewSerialTool().Execute(context.Background(), map[string]any{
		"action": "read",
		"port":   port,
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "length is required") {
		t.Fatalf("read result = %#v, want a length error", result)
	}

	result = NewSerialTool().Execute(context.Background(), map[string]any{
		"action":         "read",
		"port":           port,
		"read_until":     "\n",
		"read_available": true,
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "cannot be used together") {
		t.Fatalf("read result = %#v, want a read-mode conflict error", result)
	}
}

func TestParseSerialWritePayloadRejectsFractionalBytes(t *testing.T) {
	_, errResult := parseSerialWritePayload(map[string]any{
		"data": []any{65.9},
	})
	if errResult == nil {
		t.Fatal("expected fractional byte value to fail")
	}
}

func TestValidatePlatformSerialBaudAcceptsArbitraryOnDarwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("IOSSIOSPEED is a macOS feature")
	}
	if err := validatePlatformSerialBaud(123456); err != nil {
		t.Fatalf("validatePlatformSerialBaud(123456) error = %v", err)
	}
}

func TestValidateSerialBaud(t *testing.T) {
	tests := []struct {
		name    string
		baud    int
		wantErr bool
	}{
		{name: "default-supported", baud: 115200},
		{name: "standard-high", baud: 230400},
		{name: "common-high", baud: 460800},
		{name: "fast-high", baud: 921600},
		{name: "too-low", baud: 49, wantErr: true},
		{name: "too-high", baud: 4000001, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSerialBaud(tt.baud)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSerialBaud(%d) error = %v, wantErr %v", tt.baud, err, tt.wantErr)
			}
		})
	}
}

func TestSerialReadCanceledBeforeOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	_, err := serialRead(
		ctx,
		serialConfig{Port: port, Baud: 115200, DataBits: 8, Parity: "none", StopBits: 1},
		1,
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("serialRead() error = %v, want context canceled", err)
	}
}

func TestSerialWriteCanceledBeforeOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port := "/dev/ttyUSB0"
	if runtime.GOOS == "windows" {
		port = "COM3"
	}

	_, err := serialWrite(
		ctx,
		serialConfig{Port: port, Baud: 115200, DataBits: 8, Parity: "none", StopBits: 1},
		[]byte("AT"),
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("serialWrite() error = %v, want context canceled", err)
	}
}

func TestParseSerialConfigRejectsUnsafePortPaths(t *testing.T) {
	tests := []string{
		"../../../etc/passwd",
		"/etc/passwd",
		`C:\temp\device.txt`,
		`\\.\C:\temp\device.txt`,
	}

	for _, port := range tests {
		t.Run(strings.ReplaceAll(port, "/", "_"), func(t *testing.T) {
			_, errResult := parseSerialConfig(map[string]any{
				"port": port,
			})
			if errResult == nil {
				t.Fatalf("expected unsafe port %q to be rejected", port)
			}
		})
	}
}

func TestNormalizeUnixSerialPath(t *testing.T) {
	tests := []struct {
		port string
		want string
	}{
		{port: "ttyUSB0", want: "/dev/ttyUSB0"},
		{port: "/dev/ttyACM0", want: "/dev/ttyACM0"},
		{port: "/dev/cu.usbserial-0001", want: "/dev/cu.usbserial-0001"},
	}

	for _, tt := range tests {
		got, err := normalizeUnixSerialPath(tt.port)
		if err != nil {
			t.Fatalf("normalizeUnixSerialPath(%q) unexpected error = %v", tt.port, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeUnixSerialPath(%q) = %q, want %q", tt.port, got, tt.want)
		}
	}
}

func TestNormalizeUnixSerialPathRejectsInvalidNames(t *testing.T) {
	tests := []string{
		"",
		"ttyUSB0/../../passwd",
		"/dev/../../etc/passwd",
		"/tmp/ttyUSB0",
		"ttyUSB",
		"COM3",
	}

	for _, port := range tests {
		t.Run(strings.ReplaceAll(port, "/", "_"), func(t *testing.T) {
			if _, err := normalizeUnixSerialPath(port); err == nil {
				t.Fatalf("expected %q to be rejected", port)
			}
		})
	}
}

func TestNormalizeWindowsSerialPath(t *testing.T) {
	tests := []struct {
		port string
		want string
	}{
		{port: "COM3", want: `\\.\COM3`},
		{port: "com12", want: `\\.\COM12`},
		{port: `\\.\COM7`, want: `\\.\COM7`},
	}

	for _, tt := range tests {
		got, err := normalizeWindowsSerialPath(tt.port)
		if err != nil {
			t.Fatalf("normalizeWindowsSerialPath(%q) unexpected error = %v", tt.port, err)
		}
		if got != tt.want {
			t.Fatalf("normalizeWindowsSerialPath(%q) = %q, want %q", tt.port, got, tt.want)
		}
	}
}

func TestNormalizeWindowsSerialPathRejectsInvalidNames(t *testing.T) {
	tests := []string{
		"",
		"COM0",
		"COM",
		"/dev/ttyUSB0",
		`C:\temp\device.txt`,
		`\\.\C:\temp\device.txt`,
		`\\server\share\COM3`,
	}

	for _, port := range tests {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(port, `\`, "_"), "/", "_"), func(t *testing.T) {
			if _, err := normalizeWindowsSerialPath(port); err == nil {
				t.Fatalf("expected %q to be rejected", port)
			}
		})
	}
}

func TestParseSerialTimeout(t *testing.T) {
	timeout, errResult := parseSerialTimeout(map[string]any{
		"timeout_ms": float64(2500),
	})
	if errResult != nil {
		t.Fatalf("parseSerialTimeout() unexpected error = %v", errResult.ForLLM)
	}
	if timeout != 2500*time.Millisecond {
		t.Fatalf("timeout = %v, want 2500ms", timeout)
	}
}

func TestParseSerialWritePayloadSupportsText(t *testing.T) {
	data, errResult := parseSerialWritePayload(map[string]any{
		"text": "AT\r\n",
	})
	if errResult != nil {
		t.Fatalf("parseSerialWritePayload() unexpected error = %v", errResult.ForLLM)
	}
	if string(data) != "AT\r\n" {
		t.Fatalf("payload = %q, want %q", string(data), "AT\r\n")
	}
}

func TestParseSerialWritePayloadRejectsOutOfRangeByte(t *testing.T) {
	_, errResult := parseSerialWritePayload(map[string]any{
		"data": []any{float64(256)},
	})
	if errResult == nil {
		t.Fatal("expected payload validation failure")
	}
}
