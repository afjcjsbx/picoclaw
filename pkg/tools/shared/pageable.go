package toolshared

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// MCPResultMetadataTag delimits the sibling fields kept for the model when
	// an MCP server's JSON text payload is rewritten as the pageable text it
	// wraps.
	MCPResultMetadataTag        = "<mcp-result-metadata>"
	MCPResultMetadataClosingTag = "</mcp-result-metadata>"

	// EnvelopeMaxParseBytes bounds the payload size EnvelopeSpec unwrapping is
	// willing to parse. Unwrapping copies the result into a map and then into a
	// string, so a multi-megabyte JSON object would be duplicated in memory for
	// no practical gain; oversized payloads are returned verbatim.
	EnvelopeMaxParseBytes = 16 << 20
)

// EnvelopeSpec describes one recognized JSON envelope shape.
//
// Keys is the complete key set the object may contain. TextKey names the
// single string member that holds the pageable payload. Validate, when set,
// runs after the key-set check and can require discriminator fields; the text
// member is always checked for a non-empty string by the shared renderer.
type EnvelopeSpec struct {
	Keys     map[string]struct{}
	TextKey  string
	OpenTag  string
	CloseTag string
	Validate func(map[string]json.RawMessage) bool
}

// mcpResultEnvelopeKeys is the complete key set recognized as an MCP text
// payload: the shape some servers emit when they deliver a JSON payload as
// their text content ({"result": <text>, ...}). A JSON object whose keys stay
// inside this set with a non-empty string "result" is treated as that payload;
// anything else is left verbatim.
var mcpResultEnvelopeKeys = map[string]struct{}{
	"result":            {},
	"structuredContent": {},
	"_meta":             {},
}

// MCPEnvelopeSpec is the envelope shape recognized for MCP tools. It is
// exported so the generic spill path can try the MCP shape and the web_fetch
// shape in one parse, without a second allocation of the same payload.
var MCPEnvelopeSpec = EnvelopeSpec{
	Keys:     mcpResultEnvelopeKeys,
	TextKey:  "result",
	OpenTag:  MCPResultMetadataTag,
	CloseTag: MCPResultMetadataClosingTag,
}

// PageableMCPServerText rewrites a recognized MCP text payload —
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
	return UnwrapEnvelopes(content, MCPEnvelopeSpec)
}

// UnwrapEnvelope rewrites content as pageable text when it matches spec.
// It returns content unchanged otherwise.
func UnwrapEnvelope(content string, spec EnvelopeSpec) string {
	return UnwrapEnvelopes(content, spec)
}

// UnwrapEnvelopes parses content once and returns the pageable text for the
// first matching spec. It returns content unchanged when no spec matches or
// when content is not a JSON object within EnvelopeMaxParseBytes.
func UnwrapEnvelopes(content string, specs ...EnvelopeSpec) string {
	payload, ok := parseEnvelopeObject(content)
	if !ok {
		return content
	}
	for _, spec := range specs {
		if !hasOnlyKeys(payload, spec.Keys) {
			continue
		}
		if spec.Validate != nil && !spec.Validate(payload) {
			continue
		}
		if text, ok := renderEnvelopeText(payload, spec); ok {
			return text
		}
	}
	return content
}

// parseEnvelopeObject applies the cheap pre-parse guards and decodes one JSON
// object. Plain text, JSON arrays, and oversized payloads are rejected before
// any map allocation.
func parseEnvelopeObject(content string) (map[string]json.RawMessage, bool) {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" || trimmed[0] != '{' || len(content) > EnvelopeMaxParseBytes {
		return nil, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload) == 0 {
		return nil, false
	}
	return payload, true
}

func hasOnlyKeys(payload map[string]json.RawMessage, keys map[string]struct{}) bool {
	for key := range payload {
		if _, ok := keys[key]; !ok {
			return false
		}
	}
	return true
}

// renderEnvelopeText extracts the spec's text member and appends the remaining
// fields in a metadata block. The bool is false when the text member is missing,
// empty, or not a string. A metadata marshal failure degrades to the bare text
// rather than losing the recognized payload.
func renderEnvelopeText(payload map[string]json.RawMessage, spec EnvelopeSpec) (string, bool) {
	var text string
	if err := json.Unmarshal(payload[spec.TextKey], &text); err != nil || text == "" {
		return "", false
	}
	extras := make(map[string]json.RawMessage, len(payload)-1)
	for key, value := range payload {
		if key != spec.TextKey {
			extras[key] = value
		}
	}
	if len(extras) == 0 {
		return text, true
	}
	metadata, err := json.MarshalIndent(extras, "", "  ")
	if err != nil {
		return text, true
	}
	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n", text, spec.OpenTag, metadata, spec.CloseTag), true
}
