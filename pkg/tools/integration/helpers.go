package integrationtools

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	largeBase64OmittedMessage = toolshared.LargeBase64OmittedMessage
	inlineMediaOmittedMessage = toolshared.InlineMediaOmittedMessage
)

func getInt64Arg(args map[string]any, key string, defaultVal int64) (int64, error) {
	raw, exists := args[key]
	if !exists {
		return defaultVal, nil
	}

	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be an integer, got float %v", key, v)
		}
		if v > math.MaxInt64 || v < math.MinInt64 {
			return 0, fmt.Errorf("%s value %v overflows int64", key, v)
		}
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid integer format for %s parameter: %w", key, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported type %T for %s parameter", raw, key)
	}
}
