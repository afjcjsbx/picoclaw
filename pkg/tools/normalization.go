package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	largeBase64OmittedMessage = toolshared.LargeBase64OmittedMessage
	inlineMediaOmittedMessage = toolshared.InlineMediaOmittedMessage
	inlineMediaStoredMessage  = "[Tool returned inline media content (%s); omitted from model context and registered as a media attachment.]"

	// largeResultSpillSentinel prefixes a ForLLM value that has been reduced to a
	// bounded artifact preview, so the model and logs can recognize it. It is
	// informational only: idempotency is tracked by ToolResult.LargeResultSpilled,
	// because tool output could contain this text.
	largeResultSpillSentinel = "[picoclaw-large-tool-result]"

	// largeResultPreviewHeadRunes and largeResultPreviewTailRunes bound the
	// inline head/tail preview kept for a spilled result. Together they stay
	// within the intended ~2-4KB preview budget: the model usually has enough
	// content to answer without re-reading the artifact, which would otherwise
	// re-spill and loop.
	largeResultPreviewHeadRunes = 2048
	largeResultPreviewTailRunes = 1024

	// webFetchMetadataTag delimits the sibling fields kept for the model when a
	// web_fetch envelope is spilled as its pageable text. The values survive in
	// the artifact; only the JSON envelope around them is replaced.
	webFetchMetadataTag        = "<web-fetch-metadata>"
	webFetchMetadataClosingTag = "</web-fetch-metadata>"

	// webFetchEnvelopeMaxParseBytes bounds the payload size pageableToolResultText
	// is willing to parse. Unwrapping copies the result into a map and then into
	// a string, so a multi-megabyte JSON object from exec or a hook would be
	// duplicated in memory for no practical gain. web_fetch itself is bounded by
	// its download limit (10 MiB by default) and maxChars, so this only affects
	// pathological payloads; those spill verbatim, as before the unwrap existed.
	webFetchEnvelopeMaxParseBytes = 16 << 20
)

var (
	inlineMarkdownDataURLRe = regexp.MustCompile(`!\[[^\]]*\]\((data:[^)]+)\)`)
	inlineRawDataURLRe      = regexp.MustCompile(`data:[^;\s]+;base64,[A-Za-z0-9+/=\r\n]+`)

	// webFetchEnvelopeKeys is the complete key set of the web_fetch result
	// envelope (pkg/tools/integration/web.go). A JSON object whose keys stay
	// inside this set with a non-empty string "text" is that tool's own
	// envelope, never an arbitrary tool's JSON payload.
	webFetchEnvelopeKeys = map[string]struct{}{
		"url":       {},
		"status":    {},
		"extractor": {},
		"truncated": {},
		"length":    {},
		"text":      {},
	}
)

func normalizeToolResult(
	result *ToolResult,
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
	workspace string,
	maxInlineResultRunes int,
) *ToolResult {
	if result == nil {
		return nil
	}

	// Work on a shallow copy so the tool-owned result is never mutated in
	// place. A tool that returns a cached/shared *ToolResult would otherwise
	// accumulate media refs and re-emit them on subsequent calls.
	normalized := *result
	if len(result.Media) > 0 {
		normalized.Media = append([]string(nil), result.Media...)
	}
	if len(result.ArtifactTags) > 0 {
		normalized.ArtifactTags = append([]string(nil), result.ArtifactTags...)
	}
	result = &normalized

	notes := make([]string, 0, 2)
	seen := make(map[string]struct{})

	if store != nil && channel != "" && chatID != "" {
		var refs []string
		var extractedNotes []string

		result.ForLLM, refs, extractedNotes = extractInlineMediaRefs(
			result.ForLLM,
			toolName,
			store,
			channel,
			chatID,
			seen,
		)
		result.Media = append(result.Media, refs...)
		notes = append(notes, extractedNotes...)

		result.ForUser, refs, extractedNotes = extractInlineMediaRefs(
			result.ForUser,
			toolName,
			store,
			channel,
			chatID,
			seen,
		)
		result.Media = append(result.Media, refs...)
		notes = append(notes, extractedNotes...)
	}

	result.ForLLM = toolshared.SanitizeToolLLMContent(result.ForLLM)

	if len(result.Media) > 0 && len(notes) > 0 {
		if strings.TrimSpace(result.ForLLM) == "" {
			result.ForLLM = strings.Join(notes, "\n")
		} else {
			result.ForLLM = strings.TrimSpace(result.ForLLM) + "\n" + strings.Join(notes, "\n")
		}
	}
	if len(result.Media) > 0 && strings.TrimSpace(result.ForLLM) == "" {
		result.ForLLM = "[Tool returned media content; omitted from model context and registered as a media attachment.]"
	}

	persistLargeResultArtifact(result, toolName, workspace, maxInlineResultRunes)

	return result
}

// persistLargeResultArtifact spills an oversized tool result to a workspace
// artifact so the full payload does not consume model context. Unlike a plain
// pointer, ForLLM keeps a bounded head+tail preview plus an honest, front-loaded
// truncation notice. The artifact path is exposed once, via ArtifactTags, which
// ContentForLLM renders for the model. Keeping useful content inline means the
// model can usually answer from the preview instead of re-reading the artifact
// — which for content tools such as read_file would spill again and loop. It is
// a no-op when the result is small, already spilled, the workspace is unknown,
// or persistence is disabled.
//
// When a tool cut its own output before returning (result.FullText is set),
// that pre-truncation text is spilled and previewed instead of the truncated
// view, so the omitted middle stays readable from the artifact.
//
// Security note: this runs before the sensitive-data filter is applied, so the
// artifact file on disk keeps the unredacted tool output — verbatim, or the
// normalized pageable text for a recognized JSON envelope. Only the content
// injected into the model context is filtered. Treat workspace artifacts as
// sensitive data. See docs/security/sensitive_data_filtering.md.
func persistLargeResultArtifact(result *ToolResult, toolName, workspace string, maxRunes int) {
	if result == nil || maxRunes <= 0 || workspace == "" {
		return
	}
	if result.ResponseHandled {
		// The tool already delivered its payload to the user; the model only
		// needs the short confirmation.
		return
	}
	if result.LargeResultSpilled {
		// Already reduced to a bounded preview: never spill a preview again.
		return
	}

	text := strings.TrimSpace(result.ForLLM)
	if fullText := strings.TrimSpace(result.FullText); fullText != "" {
		// The tool truncated its own output before returning it (exec,
		// web_fetch). Spill and preview the pre-truncation text so the omitted
		// middle is not lost together with the tool's notice.
		text = fullText
	}
	result.FullText = ""

	size := utf8.RuneCountInString(text)
	if text == "" || size <= maxRunes {
		return
	}

	// A recognized JSON envelope (today only web_fetch) is spilled as the
	// pageable text it wraps. The size decision above stays on the raw result
	// — that is what would have cost context — but the artifact and preview
	// both carry real newlines instead of one escaped line.
	if pageable := pageableToolResultText(text, toolName); pageable != text {
		text = pageable
		size = utf8.RuneCountInString(text)
		if size <= maxRunes {
			// Unwrapping, not truncation, brought the result back under the
			// inline budget: keep it whole and artifact-free.
			result.ForLLM = text
			return
		}
	}

	preview, omitted, headRunes, tailRunes := buildLargeResultPreview(text, size, maxRunes)

	dir := filepath.Join(workspace, ".artifacts", "tool")
	pattern := toolshared.SanitizeIdentifierComponent(toolName) + "_*.txt"
	path, err := toolshared.PersistTextArtifact(dir, pattern, text)
	if err != nil {
		logger.WarnCF("tool", "Failed to persist large tool result artifact", map[string]any{
			"tool":  toolName,
			"chars": size,
			"error": err.Error(),
		})
		// Keep a bounded preview even when the full output could not be saved:
		// dropping everything would hide actionable content and can make the
		// model retry identically.
		result.ForLLM = fmt.Sprintf(
			"%s [Tool returned a large result (%d chars, %d omitted); "+
				"showing a bounded preview because artifact persistence failed.]\n\n%s",
			largeResultSpillSentinel, size, omitted, preview,
		)
		result.LargeResultSpilled = true
		return
	}

	result.ForLLM = fmt.Sprintf(
		"%s [Tool returned a large result (%d chars, %d omitted); "+
			"showing first %d and last %d characters.]\n\n%s\n\n"+
			"💾 [Full output saved as a local artifact (path listed below) — "+
			"use read_file (offset/length) to read more.]",
		largeResultSpillSentinel, size, omitted, headRunes, tailRunes, preview,
	)
	result.ArtifactTags = append(result.ArtifactTags, "[file:"+path+"]")
	result.LargeResultSpilled = true
}

// buildLargeResultPreview returns a head+tail preview of text, the number of
// runes omitted between the two fragments, and the head/tail sizes. The preview
// is bounded so inline context stays small while keeping the most useful parts:
// the head (which for read_file includes its native truncation/offset guidance)
// and the tail. The middle omission marker carries the real omitted count.
//
// Callers must only pass oversized text: total > maxRunes > 0. That guarantees
// headBudget+tailBudget <= maxRunes < total, so something is always omitted and
// both budgets are positive.
func buildLargeResultPreview(text string, total, maxRunes int) (preview string, omitted, headRunes, tailRunes int) {
	headBudget := largeResultPreviewHeadRunes
	tailBudget := largeResultPreviewTailRunes
	if headBudget+tailBudget > maxRunes {
		// Small caps (e.g. tests): scale the preview down so it fits.
		headBudget = maxRunes * 2 / 3
		tailBudget = maxRunes - headBudget
	}

	headRunes = headBudget
	tailRunes = tailBudget
	omitted = total - headBudget - tailBudget

	// Walk only the head and tail windows instead of converting the whole text
	// to []rune: tool output can be tens of MB while the preview is ~3KB.
	headEnd := 0
	for i := 0; i < headBudget; i++ {
		_, w := utf8.DecodeRuneInString(text[headEnd:])
		headEnd += w
	}
	tailStart := len(text)
	for i := 0; i < tailBudget; i++ {
		_, w := utf8.DecodeLastRuneInString(text[:tailStart])
		tailStart -= w
	}

	head, tail := text[:headEnd], text[tailStart:]
	return fmt.Sprintf("%s\n\n[... %d characters truncated ...]\n\n%s", head, omitted, tail),
		omitted, headRunes, tailRunes
}

// pageableToolResultText rewrites a recognized JSON envelope — the web_fetch
// result {url, status, extractor, truncated, length, text} and, for MCP tools,
// a text payload shaped like {"result": <text>, ...} — into the pageable text
// it wraps: the payload's text with real newlines, followed by the sibling
// fields in a metadata block.
//
// Persisting such an envelope verbatim would put the whole document on ONE
// line with escaped newlines, so the read_file/search_files pagination the
// spill notice recommends could not page it at all. The web_fetch envelope is
// recognized by SHAPE: a JSON object whose keys stay inside its known key set,
// with the envelope's discriminator fields present and well typed. That is
// safe because {"url": ..., "status": ..., "text": ...} is not a generic shape.
// The MCP text payload is recognized only for MCP tools (names prefixed mcp_):
// {"result": "..."} is a common generic JSON shape, and an exec or hook result
// must stay verbatim. Anything else — unparseable JSON, a non-object, an
// unknown key, a missing or mistyped discriminator, or a missing, empty, or
// non-string text field — is returned verbatim, so opaque JSON payloads from
// other tools (exec, curl, ...) keep their exact bytes and stay jq-able.
func pageableToolResultText(content, toolName string) string {
	// Cheap guards before any parse allocation. The envelope is a JSON object,
	// so plain text, log output, and JSON arrays are rejected on the first byte.
	// The size cap keeps a pathological multi-megabyte object from being parsed
	// and duplicated in memory (see webFetchEnvelopeMaxParseBytes).
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" || trimmed[0] != '{' || len(content) > webFetchEnvelopeMaxParseBytes {
		return content
	}

	if isMCPToolName(toolName) {
		if pageable := toolshared.PageableMCPServerText(content); pageable != content {
			return pageable
		}
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload) == 0 {
		return content
	}
	for key := range payload {
		if _, ok := webFetchEnvelopeKeys[key]; !ok {
			return content
		}
	}
	var pageURL string
	if err := json.Unmarshal(payload["url"], &pageURL); err != nil || pageURL == "" {
		return content
	}
	var status any
	if err := json.Unmarshal(payload["status"], &status); err != nil {
		return content
	}
	if _, ok := status.(float64); !ok {
		return content
	}
	var text string
	if err := json.Unmarshal(payload["text"], &text); err != nil || text == "" {
		return content
	}
	extras := make(map[string]json.RawMessage, len(payload)-1)
	for key, value := range payload {
		if key != "text" {
			extras[key] = value
		}
	}
	metadata, err := json.MarshalIndent(extras, "", "  ")
	if err != nil {
		return text
	}
	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n", text, webFetchMetadataTag, metadata, webFetchMetadataClosingTag)
}

// isMCPToolName reports whether toolName belongs to an MCP tool. MCPTool.Name
// always prefixes the sanitized server and tool names with "mcp_" (see
// pkg/tools/integration/mcp_tool.go), so the prefix is the stable discriminator
// between a server text payload and an arbitrary tool's JSON output.
func isMCPToolName(toolName string) bool {
	return strings.HasPrefix(toolName, "mcp_")
}

func extractInlineMediaRefs(
	text string,
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
	seen map[string]struct{},
) (cleaned string, refs []string, notes []string) {
	cleaned = text

	matches := inlineMarkdownDataURLRe.FindAllStringSubmatch(cleaned, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		dataURL := match[1]
		ref, note := storeInlineDataURL(toolName, store, channel, chatID, dataURL, seen)
		if ref != "" {
			refs = append(refs, ref)
		}
		if note != "" {
			notes = append(notes, note)
		}
		cleaned = strings.ReplaceAll(cleaned, match[0], "")
	}

	rawMatches := inlineRawDataURLRe.FindAllString(cleaned, -1)
	for _, dataURL := range rawMatches {
		ref, note := storeInlineDataURL(toolName, store, channel, chatID, dataURL, seen)
		if ref != "" {
			refs = append(refs, ref)
		}
		if note != "" {
			notes = append(notes, note)
		}
		cleaned = strings.ReplaceAll(cleaned, dataURL, "")
	}

	return strings.TrimSpace(cleaned), refs, notes
}

func storeInlineDataURL(
	toolName string,
	store media.MediaStore,
	channel string,
	chatID string,
	dataURL string,
	seen map[string]struct{},
) (ref string, note string) {
	dataURL = strings.TrimSpace(dataURL)
	if _, ok := seen[dataURL]; ok {
		return "", ""
	}
	seen[dataURL] = struct{}{}

	if !strings.HasPrefix(strings.ToLower(dataURL), "data:") {
		return "", ""
	}

	comma := strings.IndexByte(dataURL, ',')
	if comma <= 5 {
		return "", "[Tool returned inline media content that could not be parsed.]"
	}

	metaPart := dataURL[:comma]
	payload := dataURL[comma+1:]
	if !strings.Contains(strings.ToLower(metaPart), ";base64") {
		return "", "[Tool returned inline media content that was not base64-encoded.]"
	}

	mimeType := strings.TrimSpace(strings.TrimPrefix(metaPart, "data:"))
	if semi := strings.IndexByte(mimeType, ';'); semi >= 0 {
		mimeType = mimeType[:semi]
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	payload = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(payload)
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) that could not be decoded.]", mimeType)
	}

	dir := media.TempDir()
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}

	ext := toolshared.ExtensionForMIMEType(mimeType)
	tmpFile, err := os.CreateTemp(dir, "tool-inline-*"+ext)
	if err != nil {
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}
	tmpPath := tmpFile.Name()
	if _, err = tmpFile.Write(decoded); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be stored.]", mimeType)
	}

	filename := toolshared.SanitizeIdentifierComponent(toolName) + ext
	scope := fmt.Sprintf(
		"tool:inline:%s:%s:%s:%d",
		toolshared.SanitizeIdentifierComponent(toolName),
		channel,
		chatID,
		time.Now().UnixNano(),
	)

	ref, err = store.Store(tmpPath, media.MediaMeta{
		Filename:    filename,
		ContentType: mimeType,
		Source:      fmt.Sprintf("tool:inline:%s", toolshared.SanitizeIdentifierComponent(toolName)),
	}, scope)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Sprintf("[Tool returned inline media content (%s) but it could not be registered.]", mimeType)
	}

	return ref, fmt.Sprintf(inlineMediaStoredMessage, mimeType)
}
