package hardwaretools

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// isValidBusID checks that a bus identifier is a simple number (prevents path
// injection through /dev/i2c-*).
func isValidBusID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseI2CAddress extracts and validates the 7-bit device address.
func parseI2CAddress(args map[string]any) (int, *ToolResult) {
	addr, ok, err := parseI2CByteArg(args, "address")
	if err != nil {
		return 0, ErrorResult(err.Error())
	}
	if !ok {
		return 0, ErrorResult("address is required (e.g. 0x38 for AHT20)")
	}
	if addr < 0x03 || addr > 0x77 {
		return 0, ErrorResult("address must be in valid 7-bit range (0x03-0x77)")
	}
	return addr, nil
}

// parseI2CBus extracts and validates an I2C bus from args. It accepts both
// "1" and the numeric form 1 so a schema-following model and a hand-written
// call agree.
func parseI2CBus(args map[string]any) (string, *ToolResult) {
	raw, ok := args["bus"]
	if !ok || raw == nil {
		return "", ErrorResult("bus is required (e.g. \"1\" for /dev/i2c-1)")
	}

	var bus string
	switch v := raw.(type) {
	case string:
		bus = strings.TrimSpace(v)
	default:
		value, err := parseI2CScalar(raw, "bus")
		if err != nil {
			return "", ErrorResult(err.Error())
		}
		bus = strconv.Itoa(value)
	}
	if !isValidBusID(bus) {
		return "", ErrorResult("invalid bus identifier: must be a number (e.g. \"1\")")
	}
	return bus, nil
}

// parseI2CScalar accepts the numeric forms an LLM commonly emits for a byte:
// a JSON number, a decimal string, or a hexadecimal string such as "0x38".
func parseI2CScalar(value any, field string) (int, error) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be an integer value", field)
		}
		if v > float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, fmt.Errorf("%s is out of range", field)
		}
		return int(v), nil
	case float32:
		return parseI2CScalar(float64(v), field)
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		if v > int64(math.MaxInt) || v < int64(math.MinInt) {
			return 0, fmt.Errorf("%s is out of range", field)
		}
		return int(v), nil
	case uint:
		if uint64(v) > uint64(math.MaxInt) {
			return 0, fmt.Errorf("%s is out of range", field)
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		if uint64(v) > uint64(math.MaxInt) {
			return 0, fmt.Errorf("%s is out of range", field)
		}
		return int(v), nil
	case uint64:
		if v > uint64(math.MaxInt) {
			return 0, fmt.Errorf("%s is out of range", field)
		}
		return int(v), nil
	case json.Number:
		parsed, err := strconv.ParseInt(v.String(), 0, strconv.IntSize)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer value: %v", field, err)
		}
		return int(parsed), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, fmt.Errorf("%s must not be empty", field)
		}
		parsed, err := strconv.ParseInt(s, 0, strconv.IntSize)
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer value such as 56 or \"0x38\"", field)
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("%s must be an integer or an integer string such as \"0x38\"", field)
	}
}

// parseI2CByteArg parses an optional scalar argument and enforces 0-255.
func parseI2CByteArg(args map[string]any, key string) (int, bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, false, nil
	}
	value, err := parseI2CScalar(raw, key)
	if err != nil {
		return 0, false, err
	}
	if value < 0 || value > 255 {
		return 0, false, fmt.Errorf("%s = %d is out of byte range (0-255)", key, value)
	}
	return value, true, nil
}

// parseI2CLength accepts both the current "length" name and the historical
// "count" name documented for older releases. Supplying both with different
// values is an error instead of silently preferring one.
func parseI2CLength(args map[string]any) (int, error) {
	length, hasLength, err := parseI2CPositiveInt(args, "length")
	if err != nil {
		return 0, err
	}
	count, hasCount, err := parseI2CPositiveInt(args, "count")
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
		length = 1
	}
	if length < 1 || length > 256 {
		return 0, fmt.Errorf("length must be between 1 and 256")
	}
	return length, nil
}

func parseI2CPositiveInt(args map[string]any, key string) (int, bool, error) {
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

// parseI2CDelayMS parses the optional delay between a command write and the
// following read (or after a write). It is bounded to keep a tool call from
// sleeping for an unbounded amount of time.
func parseI2CDelayMS(args map[string]any) (int, error) {
	raw, ok := args["delay_ms"]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := parseI2CScalar(raw, "delay_ms")
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 60000 {
		return 0, fmt.Errorf("delay_ms must be between 0 and 60000")
	}
	return value, nil
}

// parseI2CRepeatedStart parses the optional repeated-start switch.
func parseI2CRepeatedStart(args map[string]any) (bool, error) {
	raw, ok := args["repeated_start"]
	if !ok || raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("repeated_start must be a boolean")
	}
	return value, nil
}

// parseI2CData accepts an array of numbers/strings or a whitespace/comma
// separated byte string. The latter keeps hand-written sensor sequences
// compact and mirrors how register maps are usually documented.
func parseI2CData(args map[string]any, key string) ([]byte, bool, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false, nil
	}

	if text, isString := raw.(string); isString {
		fields := strings.FieldsFunc(text, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == ':' || r == '-'
		})
		if len(fields) == 0 {
			return nil, true, fmt.Errorf("%s must contain at least one byte", key)
		}
		data := make([]byte, len(fields))
		for i, field := range fields {
			value, err := parseI2CScalar(field, fmt.Sprintf("%s[%d]", key, i))
			if err != nil {
				return nil, true, err
			}
			if value < 0 || value > 255 {
				return nil, true, fmt.Errorf("%s[%d] = %d is out of byte range (0-255)", key, i, value)
			}
			data[i] = byte(value)
		}
		return data, true, nil
	}

	items, ok := raw.([]any)
	if !ok {
		switch typed := raw.(type) {
		case []int:
			data := make([]byte, len(typed))
			for i, value := range typed {
				if value < 0 || value > 255 {
					return nil, true, fmt.Errorf("%s[%d] = %d is out of byte range (0-255)", key, i, value)
				}
				data[i] = byte(value)
			}
			return data, true, nil
		case []byte:
			return append([]byte(nil), typed...), true, nil
		default:
			return nil, true, fmt.Errorf("%s must be an array of byte values or a byte string", key)
		}
	}

	data := make([]byte, len(items))
	for i, item := range items {
		value, err := parseI2CScalar(item, fmt.Sprintf("%s[%d]", key, i))
		if err != nil {
			return nil, true, err
		}
		if value < 0 || value > 255 {
			return nil, true, fmt.Errorf("%s[%d] = %d is out of byte range (0-255)", key, i, value)
		}
		data[i] = byte(value)
	}
	return data, true, nil
}
