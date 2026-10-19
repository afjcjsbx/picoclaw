package fstools

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

// LoadImageTool loads a local image file into the MediaStore and returns a
// media:// reference. The agent loop's resolveMediaRefs will then base64-encode
// it and attach it as an image_url part in the next LLM request, enabling
// vision on local files — the same pipeline used when a user sends an image
// through a chat channel.
//
// This is intentionally different from SendFileTool:
//   - SendFileTool  → MediaResult + WithResponseHandled() → sends file to user, ends turn
//   - LoadImageTool → plain ToolResult with media:// in ForLLM  → LLM sees the image next turn
type LoadImageTool struct {
	workspace   string
	restrict    bool
	maxFileSize int
	mediaStore  media.MediaStore
	allowPaths  []*regexp.Regexp

	defaultChannel string
	defaultChatID  string
}

func NewLoadImageTool(
	workspace string,
	restrict bool,
	maxFileSize int,
	store media.MediaStore,
	allowPaths ...[]*regexp.Regexp,
) *LoadImageTool {
	if maxFileSize <= 0 {
		maxFileSize = config.DefaultMaxMediaSize
	}
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &LoadImageTool{
		workspace:   workspace,
		restrict:    restrict,
		maxFileSize: maxFileSize,
		mediaStore:  store,
		allowPaths:  patterns,
	}
}

func (t *LoadImageTool) Name() string { return "load_image" }

func (t *LoadImageTool) Description() string {
	return "Load a local image file so you can analyze its contents with vision. " +
		"Supported formats: JPEG, PNG, GIF, WebP, BMP. " +
		"After calling this tool, describe or analyze the image in your next response."
}

func (t *LoadImageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the local image file. Relative paths are resolved from workspace.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *LoadImageTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
}

func (t *LoadImageTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

func (t *LoadImageTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, errResult := requirePath(args)
	if errResult != nil {
		return errResult
	}

	channel, chatID, errResult := mediaTarget(ctx, t.defaultChannel, t.defaultChatID)
	if errResult != nil {
		return errResult
	}

	if t.mediaStore == nil {
		return ErrorResult("media store not configured")
	}

	resolved, errResult := resolveMediaFile(
		path, t.workspace, t.restrict, t.allowPaths, t.maxFileSize, "an image file",
	)
	if errResult != nil {
		return errResult
	}

	// Detect MIME type — reuse the helper already in send_file.go
	mediaType := detectMediaType(resolved)
	if !strings.HasPrefix(mediaType, "image/") {
		return ErrorResult(fmt.Sprintf(
			"file does not appear to be an image (detected type: %s)", mediaType,
		))
	}

	filename := filepath.Base(resolved)
	scope := fmt.Sprintf("tool:load_image:%s:%s", channel, chatID)

	ref, err := storeMediaRef(t.mediaStore, resolved, filename, mediaType, "tool:load_image", scope)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to register image in media store: %v", err)).WithError(err)
	}

	// Build the tool result text. The media:// ref in Media will be picked
	// up by resolveMediaRefs in agent_media.go and base64-encoded for tool
	// result messages (role="tool"), so the LLM can see the image content.
	msg := fmt.Sprintf("Image loaded: %s\n[image: photo]", filename)

	return &ToolResult{
		ForLLM:  msg,
		ForUser: fmt.Sprintf("Loaded image: %s", filename),
		// Media refs inside ForLLM are resolved by resolveMediaRefs in the
		// agent loop before the next LLM call. Do NOT use MediaResult here —
		// that would send the file to the user channel instead.
		Media: []string{ref},
	}
}
