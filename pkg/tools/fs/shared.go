package fstools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/media"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type ToolResult = toolshared.ToolResult

// requirePath reads the mandatory "path" argument of a media tool.
func requirePath(args map[string]any) (string, *ToolResult) {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", ErrorResult("path is required")
	}
	return path, nil
}

// mediaTarget picks the channel/chatID a media tool delivers to, preferring the
// context-injected values over the ones configured on the tool.
func mediaTarget(
	ctx context.Context,
	defaultChannel, defaultChatID string,
) (channel, chatID string, errResult *ToolResult) {
	channel = ToolChannel(ctx)
	if channel == "" {
		channel = defaultChannel
	}
	chatID = ToolChatID(ctx)
	if chatID == "" {
		chatID = defaultChatID
	}
	if channel == "" || chatID == "" {
		return "", "", ErrorResult("no target channel/chat available")
	}
	return channel, chatID, nil
}

// resolveMediaFile validates the path argument of a media tool and returns the
// readable path. expectedKind names what the caller accepts in the directory
// error message ("a file", "an image file").
func resolveMediaFile(
	path, workspace string,
	restrict bool,
	allowPaths []*regexp.Regexp,
	maxFileSize int,
	expectedKind string,
) (string, *ToolResult) {
	resolved, err := ValidateReadablePathWithAllowPaths(path, workspace, restrict, allowPaths)
	if err != nil {
		return "", ErrorResult(fmt.Sprintf("invalid path: %v", err))
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", ErrorResult(fmt.Sprintf("file not found: %v", err))
	}
	if info.IsDir() {
		return "", ErrorResult(fmt.Sprintf("path is a directory, expected %s", expectedKind))
	}
	if info.Size() > int64(maxFileSize) {
		return "", ErrorResult(fmt.Sprintf(
			"file too large: %d bytes (max %d bytes)", info.Size(), maxFileSize,
		))
	}
	return resolved, nil
}

// storeMediaRef registers a resolved file in the media store with the policy
// shared by the media tools, returning its media:// reference.
func storeMediaRef(
	store media.MediaStore,
	resolved, filename, mediaType, source, scope string,
) (string, error) {
	return store.Store(resolved, media.MediaMeta{
		Filename:      filename,
		ContentType:   mediaType,
		Source:        source,
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, scope)
}

func WithToolContext(ctx context.Context, channel, chatID string) context.Context {
	return toolshared.WithToolContext(ctx, channel, chatID)
}

func ToolChannel(ctx context.Context) string {
	return toolshared.ToolChannel(ctx)
}

func ToolChatID(ctx context.Context) string {
	return toolshared.ToolChatID(ctx)
}

func ErrorResult(message string) *ToolResult {
	return toolshared.ErrorResult(message)
}

func NewToolResult(forLLM string) *ToolResult {
	return toolshared.NewToolResult(forLLM)
}

func SilentResult(forLLM string) *ToolResult {
	return toolshared.SilentResult(forLLM)
}

func DiffResult(path string, before, after []byte) *ToolResult {
	return toolshared.DiffResult(path, before, after)
}

func MediaResult(forLLM string, mediaRefs []string) *ToolResult {
	return toolshared.MediaResult(forLLM, mediaRefs)
}
