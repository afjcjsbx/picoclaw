package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/audio/asr"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/media"
)

type failingTranscriber struct{}

func (failingTranscriber) Name() string { return "failing" }

func (failingTranscriber) Transcribe(context.Context, string) (*asr.TranscriptionResponse, error) {
	return nil, errors.New("asr backend unavailable")
}

// placeholderRecordingManager records SendPlaceholder calls so tests can assert
// that audio messages always produce visible feedback.
type placeholderRecordingManager struct {
	recordingChannelManager
	placeholders []string
}

func (m *placeholderRecordingManager) SendPlaceholder(_ context.Context, channel, chatID string) bool {
	m.placeholders = append(m.placeholders, channel+":"+chatID)
	return true
}

func TestPrepareInboundMessageForAgent_SendsPlaceholderForAudioWithoutTranscriber(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	cm := &placeholderRecordingManager{}
	al.channelManager = cm
	// No transcriber configured: transcription is skipped, but the user must
	// still see the placeholder that the channel deferred for audio messages.

	msg := testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "[voice]",
	})

	al.prepareInboundMessageForAgent(context.Background(), msg)

	if len(cm.placeholders) != 1 {
		t.Fatalf("placeholder calls = %v, want exactly one", cm.placeholders)
	}
	if cm.placeholders[0] != "telegram:chat-1" {
		t.Fatalf("placeholder target = %q, want %q", cm.placeholders[0], "telegram:chat-1")
	}
}

func TestPrepareInboundMessageForAgent_NoPlaceholderForText(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	cm := &placeholderRecordingManager{}
	al.channelManager = cm

	msg := testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "just a plain text message",
	})

	al.prepareInboundMessageForAgent(context.Background(), msg)

	if len(cm.placeholders) != 0 {
		t.Fatalf("placeholder calls = %v, want none for a text message", cm.placeholders)
	}
}

// The channel defers the placeholder for any message whose content carries an
// audio annotation and relies on the agent to send it afterwards. That must
// hold even when every transcription fails and the annotation is left in place,
// otherwise the user would get no feedback at all.
func TestPrepareInboundMessageForAgent_SendsPlaceholderWhenTranscriptionFails(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	cm := &placeholderRecordingManager{}
	al.channelManager = cm

	audioPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(audioPath, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio fixture: %v", err)
	}
	store := media.NewFileMediaStore()
	ref, err := store.Store(audioPath, media.MediaMeta{
		Filename:      "voice.ogg",
		ContentType:   "audio/ogg",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "scope-voice")
	if err != nil {
		t.Fatalf("store audio fixture: %v", err)
	}
	al.SetMediaStore(store)
	al.SetTranscriber(failingTranscriber{})

	msg := testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "[voice]",
		Media:    []string{ref},
	})

	got := al.prepareInboundMessageForAgent(context.Background(), msg)

	if len(cm.placeholders) != 1 {
		t.Fatalf("placeholder calls = %v, want exactly one", cm.placeholders)
	}
	if !strings.Contains(got.Content, "[voice]") {
		t.Fatalf("content = %q, want the untranscribed annotation preserved", got.Content)
	}
}
