package loop

import (
	"context"
	"fmt"
	"strings"

	agentcore "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/audio/tts"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func (al *AgentLoop) voiceMode(sessionKey string) string {
	if value, ok := al.voiceModes.Load(strings.TrimSpace(sessionKey)); ok {
		if mode, ok := value.(string); ok {
			return mode
		}
	}
	return "off"
}

func (al *AgentLoop) currentTTSProvider() tts.TTSProvider {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.ttsProvider
}

func (al *AgentLoop) setVoiceMode(sessionKey, mode string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("voice mode unavailable in current session")
	}
	if mode != "off" && mode != "on" && mode != "tts" {
		return fmt.Errorf("unknown voice mode: %s", mode)
	}
	if mode != "off" && al.currentTTSProvider() == nil {
		return fmt.Errorf("TTS unavailable: enable tools.send_tts and configure a TTS model")
	}
	al.voiceModes.Store(sessionKey, mode)
	return nil
}

func (al *AgentLoop) sendVoiceResponseIfEnabled(
	ctx context.Context,
	agent *agentcore.AgentInstance,
	opts processOptions,
	text string,
) {
	mode := al.voiceMode(opts.Dispatch.SessionKey)
	if mode == "off" || (mode == "on" && opts.Dispatch.InboundContext.Raw[metadataKeyInputAudio] != "true") {
		return
	}
	provider := al.currentTTSProvider()
	if provider == nil || al.mediaStore == nil || al.bus == nil {
		logger.WarnCF("voice-tts", "Cannot synthesize response: TTS runtime is not ready", nil)
		return
	}

	channel, chatID := opts.Dispatch.Channel(), opts.Dispatch.ChatID()
	ref, err := tts.SynthesizeAndStore(ctx, provider, al.mediaStore, text, "", channel, chatID)
	if err != nil {
		logger.WarnCF("voice-tts", "Failed to synthesize response", map[string]any{"error": err.Error()})
		return
	}

	part := bus.MediaPart{Ref: ref}
	if _, meta, resolveErr := al.mediaStore.ResolveWithMeta(ref); resolveErr == nil {
		part.Filename = meta.Filename
		part.ContentType = meta.ContentType
		part.Type = inferMediaType(meta.Filename, meta.ContentType)
	}
	outbound := bus.OutboundMediaMessage{
		Channel: channel,
		ChatID:  chatID,
		Context: outboundContextFromInbound(
			opts.Dispatch.InboundContext,
			channel,
			chatID,
			opts.Dispatch.ReplyToMessageID(),
		),
		AgentID:    agent.ID,
		SessionKey: opts.Dispatch.SessionKey,
		Scope:      outboundScopeFromSessionScope(opts.Dispatch.SessionScope),
		Parts:      []bus.MediaPart{part},
	}
	if al.channelManager != nil && channel != "" && !constants.IsInternalChannel(channel) {
		err = al.channelManager.SendMedia(ctx, outbound)
	} else {
		err = al.bus.PublishOutboundMedia(ctx, outbound)
	}
	if err != nil {
		logger.WarnCF("voice-tts", "Failed to send synthesized response", map[string]any{"error": err.Error()})
	}
}

func (al *AgentLoop) hasAudioInput(msg bus.InboundMessage) bool {
	if audioAnnotationRe.MatchString(msg.Content) {
		return true
	}
	if al.mediaStore == nil {
		return false
	}
	for _, ref := range msg.Media {
		_, meta, err := al.mediaStore.ResolveWithMeta(ref)
		if err == nil && utils.IsAudioFile(meta.Filename, meta.ContentType) {
			return true
		}
	}
	return false
}
