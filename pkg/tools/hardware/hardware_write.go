package hardwaretools

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// WriteRequest is the human-readable description used by the approval hook.
type WriteRequest struct {
	Summary string
	Target  string
}

// DescribeWrite returns a summary for a write-like hardware call. It returns
// false for reads, invalid arguments, and calls without confirm=true, so the
// approval hook never prompts for a call the tool would reject anyway.
func DescribeWrite(tool string, args map[string]any) (WriteRequest, bool) {
	if tool == "" || args == nil {
		return WriteRequest{}, false
	}
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return WriteRequest{}, false
	}

	switch tool {
	case "i2c":
		return describeI2CWrite(args)
	case "spi":
		return describeSPIWrite(args)
	case "serial":
		return describeSerialWrite(args)
	default:
		return WriteRequest{}, false
	}
}

// TargetAllowed reports whether target is covered by the configured allowlist.
// An empty allowlist permits reads and leaves the write switch as the gate.
func TargetAllowed(tool, target string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, entry := range allow {
		switch tool {
		case "i2c":
			normalized, ok := normalizeI2CAllowEntry(entry)
			if ok && normalized == target {
				return true
			}
		case "spi":
			if strings.TrimSpace(entry) == target {
				return true
			}
		case "serial":
			normalized, err := serialNormalizePort(entry)
			if err != nil {
				normalized = strings.TrimSpace(entry)
			}
			if normalized == target {
				return true
			}
		}
	}
	return false
}

func describeI2CWrite(args map[string]any) (WriteRequest, bool) {
	bus, result := parseI2CBus(args)
	if result != nil {
		return WriteRequest{}, false
	}
	addr, result := parseI2CAddress(args)
	if result != nil {
		return WriteRequest{}, false
	}
	payload, hasData, err := parseI2CData(args, "data")
	if err != nil || !hasData || len(payload) == 0 || len(payload) > 256 {
		return WriteRequest{}, false
	}
	register, hasRegister, err := parseI2CByteArg(args, "register")
	if err != nil {
		return WriteRequest{}, false
	}
	data := make([]byte, 0, len(payload)+1)
	if hasRegister {
		data = append(data, byte(register))
	}
	data = append(data, payload...)

	summary := fmt.Sprintf(
		"Tool: i2c\nTarget: bus %s, address 0x%02x\nPayload: %d bytes: %s",
		bus, addr, len(data), hardwareHexPreview(data, 128),
	)
	return WriteRequest{
		Summary: summary,
		Target:  i2cTarget(bus, addr),
	}, true
}

func describeSPIWrite(args map[string]any) (WriteRequest, bool) {
	dev, ok := parseSPIDevice(args)
	if !ok {
		return WriteRequest{}, false
	}
	segments, hasSegments, err := parseSPISegments(args)
	if err != nil {
		return WriteRequest{}, false
	}
	if !hasSegments {
		segment, ok, err := parseSPISingleSegment(args)
		if err != nil || !ok {
			return WriteRequest{}, false
		}
		segments = []spiSegment{segment}
	}

	total := 0
	for _, segment := range segments {
		total += max(len(segment.Data), max(segment.ReadLength, 0))
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "Tool: spi\nTarget: /dev/spidev%s\nSegments: %d, %d bytes total", dev, len(segments), total)
	for i, segment := range segments {
		fmt.Fprintf(&builder, "\n  segment %d: %d bytes", i, len(segment.Data))
		if len(segment.Data) > 0 {
			fmt.Fprintf(&builder, ": %s", hardwareHexPreview(segment.Data, 64))
		}
		if segment.ReadLength > 0 && segment.ReadLength != len(segment.Data) {
			fmt.Fprintf(&builder, " (read %d)", segment.ReadLength)
		}
	}
	return WriteRequest{Summary: builder.String(), Target: dev}, true
}

func describeSerialWrite(args map[string]any) (WriteRequest, bool) {
	cfg, result := parseSerialConfig(args)
	if result != nil {
		return WriteRequest{}, false
	}
	payload, result := parseSerialWritePayload(args)
	if result != nil {
		return WriteRequest{}, false
	}
	preview := hardwareHexPreview(payload, 128)
	if utf8.Valid(payload) && len(payload) <= 128 {
		preview = strconv.Quote(string(payload))
	}
	summary := fmt.Sprintf(
		"Tool: serial\nTarget: %s (%d %d%s%d, flow %s)\nPayload: %d bytes: %s",
		cfg.Port, cfg.Baud, cfg.DataBits, parityLetter(cfg.Parity), cfg.StopBits, cfg.FlowControl,
		len(payload), preview,
	)
	return WriteRequest{Summary: summary, Target: cfg.Port}, true
}

func parityLetter(parity string) string {
	switch parity {
	case "even":
		return "E"
	case "odd":
		return "O"
	default:
		return "N"
	}
}

func hardwareHexPreview(data []byte, limit int) string {
	if len(data) == 0 {
		return "(empty)"
	}
	total := len(data)
	if len(data) > limit {
		data = data[:limit]
	}
	parts := make([]string, len(data))
	for i, b := range data {
		parts[i] = fmt.Sprintf("0x%02x", b)
	}
	preview := strings.Join(parts, " ")
	if total > limit {
		preview += fmt.Sprintf(" ... (%d bytes total)", total)
	}
	return preview
}
