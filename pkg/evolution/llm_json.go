package evolution

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// decodeLLMObject returns the first top-level JSON object in content that
// decodes into T and passes accept. Prose or a fence around the object is
// fine, and a quoted example of the wrong shape is skipped whole, so its
// nested objects are never tried. When nothing qualifies it logs why at debug
// level under the given parser name and reports false, so the caller's
// fallback is visible.
func decodeLLMObject[T any](parser, content string, accept func(T) bool) (T, bool) {
	var zero T
	data := []byte(content)
	reason := "no JSON object"
	if strings.TrimSpace(content) == "" {
		reason = "empty content"
	}

	for offset := 0; offset < len(data); {
		start := bytes.IndexByte(data[offset:], '{')
		if start < 0 {
			break
		}
		start += offset

		dec := json.NewDecoder(bytes.NewReader(data[start:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			reason = "unmarshal error: " + err.Error()
			offset = start + 1
			continue
		}
		offset = start + int(dec.InputOffset())

		var value T
		if err := json.Unmarshal(raw, &value); err != nil {
			reason = "unmarshal error: " + err.Error()
			continue
		}
		if accept != nil && !accept(value) {
			reason = "validation error: object has the wrong shape"
			continue
		}
		return value, true
	}

	logger.DebugCF("evolution", "LLM response not used, taking the fallback", map[string]any{
		"parser": parser,
		"reason": reason,
	})
	return zero, false
}
