package toolshared

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// MCPResultMetadataTag delimits the sibling fields kept for the model when
	// an MCP server's JSON envelope is rewritten as the pageable text it wraps.
	MCPResultMetadataTag        = "<mcp-result-metadata>"
	MCPResultMetadataClosingTag = "</mcp-result-metadata>"

	// MCPEnvelopeMaxParseBytes bounds the payload size PageableMCPServerText is
	// willing to parse. Unwrapping copies the result into a map and then into a
	// string, so a multi-megabyte JSON object would be duplicated in memory for
	// no practical gain; oversized payloads are returned verbatim.
	MCPEnvelopeMaxParseBytes = 16 << 20
)

// mcpResultEnvelopeKeys is the complete key set recognized as an MCP tool
// result envelope: the shape servers emit when they deliver a JSON payload as
// their text content ({"result": <text>, "structuredContent": {...}, "_meta":
// {...}}). A JSON object whose keys stay inside this set with a non-empty
// string "result" is treated as that envelope; anything else is left verbatim.
var mcpResultEnvelopeKeys = map[string]struct{}{
	"result":            {},
	"structuredContent": {},
	"_meta":             {},
}

// PageableMCPServerText rewrites a recognized MCP tool result envelope —
// {"result": <text>, ...} with only known keys and a non-empty string "result"
// — into the pageable text it wraps: the result text with real newlines,
// followed by the sibling fields in a <mcp-result-metadata> block. The
// metadata block keeps structuredContent, _meta, and any other sibling intact
// instead of dropping them.
//
// Recognition is by SHAPE, not by tool name: an MCP server often returns its
// payload as a single minified JSON document, and storing that verbatim puts a
// whole report or file on ONE line with escaped newlines, so the
// read_file/search_files pagination the spill notices recommend could not page
// it at all. Anything unrecognized — unparseable JSON, a non-object, an unknown
// key, a missing/empty/non-string "result" — is returned verbatim, so opaque
// JSON payloads keep their exact bytes.
func PageableMCPServerText(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" || trimmed[0] != '{' || len(content) > MCPEnvelopeMaxParseBytes {
		return content
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload) == 0 {
		return content
	}
	for key := range payload {
		if _, ok := mcpResultEnvelopeKeys[key]; !ok {
			return content
		}
	}
	var text string
	if err := json.Unmarshal(payload["result"], &text); err != nil || text == "" {
		return content
	}
	extras := make(map[string]json.RawMessage, len(payload)-1)
	for key, value := range payload {
		if key != "result" {
			extras[key] = value
		}
	}
	if len(extras) == 0 {
		return text
	}
	metadata, err := json.MarshalIndent(extras, "", "  ")
	if err != nil {
		return text
	}
	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n", text, MCPResultMetadataTag, metadata, MCPResultMetadataClosingTag)
}
