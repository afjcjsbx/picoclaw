// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// overflowArchiveDir keeps the pre-compression transcript that the emergency
// fallback drops. It lives under the workspace so the model can read it back
// with read_file, like any other artifact.
const overflowArchiveDir = ".artifacts/context"

// overflowArchive is the durable handoff written before the emergency fallback
// drops turns. The full history is preserved even when only a summary remains
// in the session.
type overflowArchive struct {
	SessionKey string              `json:"session_key"`
	Reason     string              `json:"reason"`
	ArchivedAt time.Time           `json:"archived_at"`
	Messages   []providers.Message `json:"messages"`
}

// cloneMessagesForArchive copies the parts of a message that pruning can
// rewrite, so the archive keeps the pre-prune transcript even when the caller
// keeps mutating its own slice.
func cloneMessagesForArchive(history []providers.Message) []providers.Message {
	out := make([]providers.Message, len(history))
	for i, msg := range history {
		out[i] = msg
		if msg.Media != nil {
			out[i].Media = append([]string(nil), msg.Media...)
		}
		if msg.Attachments != nil {
			out[i].Attachments = append([]providers.Attachment(nil), msg.Attachments...)
		}
		if msg.SystemParts != nil {
			blocks := make([]providers.ContentBlock, len(msg.SystemParts))
			copy(blocks, msg.SystemParts)
			out[i].SystemParts = blocks
		}
		if msg.ToolCalls != nil {
			calls := make([]providers.ToolCall, len(msg.ToolCalls))
			for j, call := range msg.ToolCalls {
				calls[j] = call
				if call.Function != nil {
					fn := *call.Function
					calls[j].Function = &fn
				}
				if call.Arguments != nil {
					args := make(map[string]any, len(call.Arguments))
					for key, value := range call.Arguments {
						args[key] = value
					}
					calls[j].Arguments = args
				}
			}
			out[i].ToolCalls = calls
		}
		if msg.Usage != nil {
			usage := *msg.Usage
			out[i].Usage = &usage
		}
	}
	return out
}

// archiveOverflowHistory writes the transcript that the emergency fallback is
// about to shorten. The write is mandatory: the caller must not drop history
// when the original cannot be preserved first.
func archiveOverflowHistory(workspace, sessionKey string, history []providers.Message) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("workspace is empty")
	}
	payload := overflowArchive{
		SessionKey: sessionKey,
		Reason:     "overflow",
		ArchivedAt: time.Now().UTC(),
		Messages:   cloneMessagesForArchive(history),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal transcript: %w", err)
	}
	path, err := toolshared.PersistTextArtifact(
		filepath.Join(workspace, overflowArchiveDir),
		"session_overflow_*.json",
		string(data),
	)
	if err != nil {
		return "", fmt.Errorf("write transcript archive: %w", err)
	}
	return path, nil
}
