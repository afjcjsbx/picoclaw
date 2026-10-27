// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/h2non/filetype"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/memlimit"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// genericPlaceholderRegex matches generic media placeholders emitted by various
// channels: [image], [image: photo], [image: filename.jpg] — but NOT path tags
// like [image:/path/to/file] (path tags have no space after the colon).
var (
	imagePlaceholderRegex = regexp.MustCompile(`\[image(:\s+[^\]]*)?\]`)
	audioPlaceholderRegex = regexp.MustCompile(`\[audio(:\s+[^\]]*)?\]`)
	videoPlaceholderRegex = regexp.MustCompile(`\[video(:\s+[^\]]*)?\]`)
	filePlaceholderRegex  = regexp.MustCompile(`\[file(:\s+[^\]]*)?\]`)
)

func normalizeCurrentTurnStart(messages []providers.Message, currentTurnStart int) int {
	if currentTurnStart < 0 {
		return 0
	}
	if currentTurnStart > len(messages) {
		return len(messages)
	}
	return currentTurnStart
}

func currentTurnMessages(messages []providers.Message, currentTurnStart int) []providers.Message {
	currentTurnStart = normalizeCurrentTurnStart(messages, currentTurnStart)
	return messages[currentTurnStart:]
}

// resolveMediaRefs resolves media:// refs in messages.
// For user messages: images get path tags only ([image:/path]) so the LLM
// can decide whether to view them via load_image or operate on the file.
// For tool messages: images are base64-encoded and appended as a synthetic
// user message only after the contiguous tool-message block ends, so we don't
// break the tool-results-must-immediately-follow-assistant constraint that
// LLM APIs enforce.
// Only tool messages from the current turn may emit the synthetic user
// follow-up; historical tool results stay as plain path-tagged history.
// Non-image files always get path tags regardless of role.
// Original messages are never mutated. Returns a new slice when any message
// carries media; with a nil store or no media at all there is nothing to
// resolve and the input slice itself is returned, so callers must not assume
// the result is an independent copy.
func resolveMediaRefs(
	messages []providers.Message,
	store media.MediaStore,
	maxSize int,
	currentTurnStart int,
) []providers.Message {
	return resolveMediaRefsWithOptions(messages, store, mediaResolveOptions{maxSize: maxSize}, currentTurnStart)
}

// mediaResolveOptionsFor builds media resolution options from configuration.
func mediaResolveOptionsFor(cfg *config.Config) mediaResolveOptions {
	d := cfg.Agents.Defaults
	return mediaResolveOptions{
		maxSize:           d.GetMaxMediaSize(),
		maxImageDimension: d.GetVisionMaxImageDimension(),
		jpegQuality:       d.GetVisionJPEGQuality(),
	}
}

// mediaResolveOptions controls how media references are turned into model input.
type mediaResolveOptions struct {
	// maxSize is the maximum size, in bytes, of an image payload that may be
	// inlined. It applies after downscaling, so a large original that shrinks
	// below it is still sent.
	maxSize int
	// maxImageDimension is the longest-edge cap applied to inlined images.
	// 0 disables downscaling and sends the original bytes.
	maxImageDimension int
	// jpegQuality is the JPEG quality used when an oversized image is
	// recompressed. Ignored when maxImageDimension is 0.
	jpegQuality int
}

// resolveMediaRefsWithOptions is resolveMediaRefs with explicit vision options.
func resolveMediaRefsWithOptions(
	messages []providers.Message,
	store media.MediaStore,
	opts mediaResolveOptions,
	currentTurnStart int,
) []providers.Message {
	if store == nil {
		return messages
	}
	currentTurnStart = normalizeCurrentTurnStart(messages, currentTurnStart)

	// Fast path: with no media references there is nothing to resolve, so avoid
	// allocating and copying the whole message slice (this function runs over
	// the full history on every LLM iteration).
	hasMedia := false
	for i := range messages {
		if len(messages[i].Media) > 0 {
			hasMedia = true
			break
		}
	}
	if !hasMedia {
		return messages
	}

	result := make([]providers.Message, 0, len(messages))
	var pendingToolImages []string
	// encoded dedups data-URL encoding within this call so the same file is not
	// read and base64-encoded more than once.
	encoded := make(map[string]string)

	for idx, m := range messages {
		// When leaving a tool-message block, flush any accumulated images
		// as a synthetic user message.
		if m.Role != "tool" && len(pendingToolImages) > 0 {
			result = append(result, toolImageFollowUpPromptMessage(pendingToolImages))
			pendingToolImages = nil
		}

		if len(m.Media) == 0 {
			result = append(result, m)
			if idx == len(messages)-1 && len(pendingToolImages) > 0 {
				result = append(result, toolImageFollowUpPromptMessage(pendingToolImages))
				pendingToolImages = nil
			}
			continue
		}

		msg := m
		resolved := make([]string, 0, len(m.Media))
		var pathTags []string

		for _, ref := range m.Media {
			if !strings.HasPrefix(ref, "media://") {
				resolved = append(resolved, ref)
				continue
			}

			localPath, meta, err := store.ResolveWithMeta(ref)
			if err != nil {
				fields := map[string]any{
					"ref":   ref,
					"error": err.Error(),
				}
				if idx < currentTurnStart {
					logger.DebugCF("agent", "Skipped stale historical media ref", fields)
				} else {
					logger.WarnCF("agent", "Failed to resolve media ref", fields)
				}
				continue
			}

			info, err := os.Stat(localPath)
			if err != nil {
				logger.WarnCF("agent", "Failed to stat media file", map[string]any{
					"path":  localPath,
					"error": err.Error(),
				})
				continue
			}

			mime := detectMIME(localPath, meta)
			pathTags = append(pathTags, buildPathTag(mime, localPath))

			if m.Role == "tool" && idx >= currentTurnStart && strings.HasPrefix(mime, "image/") {
				dataURL, ok := encoded[localPath]
				if !ok {
					dataURL = encodeImageToDataURL(localPath, mime, info, opts)
					encoded[localPath] = dataURL
				}
				if dataURL != "" {
					pendingToolImages = append(pendingToolImages, dataURL)
				}
			}
		}

		msg.Media = resolved
		if len(pathTags) > 0 {
			msg.Content = injectPathTags(msg.Content, pathTags)
		}
		result = append(result, msg)

		// If this is the last message and we have pending images, flush them.
		if idx == len(messages)-1 && len(pendingToolImages) > 0 {
			result = append(result, toolImageFollowUpPromptMessage(pendingToolImages))
			pendingToolImages = nil
		}
	}

	return result
}

// encodeImageToDataURL base64-encodes an image file into a data URL, first
// downscaling it when a vision dimension cap is configured. The size limit
// applies to the payload actually sent, so an oversized original that
// downscales under opts.maxSize is still inlined. Returns empty string if the
// payload exceeds opts.maxSize or encoding fails.
func encodeImageToDataURL(localPath, mime string, info os.FileInfo, opts mediaResolveOptions) string {
	if dataURL, ok := downscaleToDataURL(localPath, mime, opts); ok {
		return dataURL
	}

	if info.Size() > int64(opts.maxSize) {
		logger.WarnCF("agent", "Media file too large, skipping", map[string]any{
			"path":     localPath,
			"size":     info.Size(),
			"max_size": opts.maxSize,
		})
		return ""
	}

	f, err := os.Open(localPath)
	if err != nil {
		logger.WarnCF("agent", "Failed to open media file", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return ""
	}
	defer f.Close()

	prefix := "data:" + mime + ";base64,"
	encodedLen := base64.StdEncoding.EncodedLen(int(info.Size()))
	var buf bytes.Buffer
	buf.Grow(len(prefix) + encodedLen)
	buf.WriteString(prefix)

	encoder := base64.NewEncoder(base64.StdEncoding, &buf)
	_, copyErr := io.Copy(encoder, f)
	closeErr := encoder.Close()
	if copyErr != nil {
		logger.WarnCF("agent", "Failed to encode media file", map[string]any{
			"path":  localPath,
			"error": copyErr.Error(),
		})
		return ""
	}
	if closeErr != nil {
		logger.WarnCF("agent", "Failed to close base64 encoder", map[string]any{
			"path":  localPath,
			"error": closeErr.Error(),
		})
		return ""
	}

	dataURL := buf.String()
	// Encoding a large image produces a transient allocation roughly twice the
	// data URL size; return it to the OS immediately so the resident set does
	// not stay inflated until the next scheduled GC.
	memlimit.ReleaseToOS(len(dataURL))
	return dataURL
}

func buildArtifactTags(store media.MediaStore, refs []string) []string {
	if store == nil || len(refs) == 0 {
		return nil
	}

	tags := make([]string, 0, len(refs))
	for _, ref := range refs {
		localPath, meta, err := store.ResolveWithMeta(ref)
		if err != nil {
			continue
		}
		mime := detectMIME(localPath, meta)
		tags = append(tags, buildPathTag(mime, localPath))
	}

	return tags
}

// appendArtifactTags merges media-derived tags with any tags already present on
// a tool result (for example the path of a spilled large-result artifact).
// Order is preserved and duplicates are dropped.
func appendArtifactTags(existing, added []string) []string {
	if len(added) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(added))
	merged := make([]string, 0, len(existing)+len(added))
	for _, tag := range existing {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		merged = append(merged, tag)
	}
	for _, tag := range added {
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		merged = append(merged, tag)
	}
	return merged
}

func buildProviderAttachments(store media.MediaStore, refs []string) []providers.Attachment {
	if store == nil || len(refs) == 0 {
		return nil
	}

	attachments := make([]providers.Attachment, 0, len(refs))
	for _, ref := range refs {
		attachment := providers.Attachment{Ref: ref}
		if _, meta, err := store.ResolveWithMeta(ref); err == nil {
			attachment.Filename = meta.Filename
			attachment.ContentType = meta.ContentType
			attachment.Type = inferMediaType(meta.Filename, meta.ContentType)
		}
		attachments = append(attachments, attachment)
	}

	return attachments
}

// detectMIME determines the MIME type from metadata or magic-bytes detection.
// Returns empty string if detection fails.
func detectMIME(localPath string, meta media.MediaMeta) string {
	if meta.ContentType != "" {
		return meta.ContentType
	}
	kind, err := filetype.MatchFile(localPath)
	if err != nil || kind == filetype.Unknown {
		return ""
	}
	return kind.MIME.Value
}

// buildPathTag creates a structured tag exposing the local file path.
// Tag type is derived from MIME: [image:/path], [audio:/path], [video:/path], or [file:/path].
func buildPathTag(mime, localPath string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "[image:" + localPath + "]"
	case strings.HasPrefix(mime, "audio/"):
		return "[audio:" + localPath + "]"
	case strings.HasPrefix(mime, "video/"):
		return "[video:" + localPath + "]"
	default:
		return "[file:" + localPath + "]"
	}
}

// injectPathTags replaces generic media tags in content with path-bearing versions,
// or appends if no matching generic tag is found. Channels emit a few different
// placeholder formats — [image], [image: photo], [image: filename.jpg] — so we
// match all of them via regex while leaving path tags ([image:/path]) untouched.
//
// When content is structured data (e.g., JSON from Feishu interactive cards or
// post messages), tags are only injected via placeholder replacement — never
// appended — to avoid corrupting the payload.
func injectPathTags(content string, tags []string) string {
	isStructured := looksLikeJSON(content)
	for _, tag := range tags {
		var pattern *regexp.Regexp
		switch {
		case strings.HasPrefix(tag, "[image:"):
			pattern = imagePlaceholderRegex
		case strings.HasPrefix(tag, "[audio:"):
			pattern = audioPlaceholderRegex
		case strings.HasPrefix(tag, "[video:"):
			pattern = videoPlaceholderRegex
		case strings.HasPrefix(tag, "[file:"):
			pattern = filePlaceholderRegex
		}

		if pattern != nil {
			if loc := pattern.FindStringIndex(content); loc != nil {
				content = content[:loc[0]] + tag + content[loc[1]:]
				continue
			}
		}

		if isStructured {
			content = tag + "\n" + content
			continue
		}

		if content == "" {
			content = tag
		} else {
			content += " " + tag
		}
	}
	return content
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) > 1 && s[0] == '{'
}
