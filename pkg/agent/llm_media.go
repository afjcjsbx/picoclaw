package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

var resolvedImagePathTagRegex = regexp.MustCompile(`\[image:[^\s\]][^\]]*\]`)

func isVisionUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// OpenRouter (and OpenAI-compatible) style.
	if strings.Contains(msg, "no endpoints found that support image input") {
		return true
	}

	// Common provider variants.
	if strings.Contains(msg, "does not support image input") ||
		strings.Contains(msg, "does not support image inputs") ||
		strings.Contains(msg, "does not support images") ||
		strings.Contains(msg, "image input is not supported") ||
		strings.Contains(msg, "images are not supported") ||
		strings.Contains(msg, "does not support vision") ||
		strings.Contains(msg, "unsupported content type: image_url") {
		return true
	}

	// Some providers return a generic "invalid" message that still mentions image_url.
	if strings.Contains(msg, "image_url") && strings.Contains(msg, "invalid") {
		return true
	}

	// DeepSeek and other strict providers reject the image_url field at the
	// JSON schema level with an "unknown variant" error rather than a semantic
	// "not supported" message.
	if strings.Contains(msg, "unknown variant") && strings.Contains(msg, "image_url") {
		return true
	}

	return false
}

func visionUnsupportedModelError(modelName string, imageModelConfigured bool) error {
	modelName = strings.TrimSpace(modelName)
	if imageModelConfigured {
		if modelName != "" {
			return fmt.Errorf(
				"selected vision model %q does not support image input; update agents.defaults.image_model to a multimodal model",
				modelName,
			)
		}
		return fmt.Errorf(
			"selected vision model does not support image input; update agents.defaults.image_model to a multimodal model",
		)
	}
	if modelName != "" {
		return fmt.Errorf(
			"active model %q does not support image input; configure agents.defaults.image_model with a multimodal model",
			modelName,
		)
	}
	return fmt.Errorf(
		"the active model does not support image input; configure agents.defaults.image_model with a multimodal model",
	)
}

func sameCandidateSet(a, b []providers.FallbackCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].StableKey() != b[i].StableKey() {
			return false
		}
	}
	return true
}

func messagesContainCurrentTurnMediaTurn(messages []providers.Message) bool {
	for _, msg := range messages {
		if len(msg.Media) > 0 {
			return true
		}
		if resolvedImagePathTagRegex.MatchString(msg.Content) {
			return true
		}
	}
	return false
}

func (p *Pipeline) routeMediaTurn(ts *turnState, exec *turnExecution) error {
	if p == nil || ts == nil || ts.agent == nil || exec == nil || exec.imageRouteDisabled ||
		!messagesContainCurrentTurnMediaTurn(currentTurnMessages(exec.callMessages, exec.currentTurnStart)) {
		return nil
	}

	var targetCandidates []providers.FallbackCandidate
	var targetModelName string
	var routeReason string

	switch {
	case len(ts.agent.ImageCandidates) > 0:
		targetCandidates = append([]providers.FallbackCandidate(nil), ts.agent.ImageCandidates...)
		targetModelName = strings.TrimSpace(p.Cfg.Agents.Defaults.ImageModel)
		routeReason = "configured_image_model"
	case exec.usedLight && len(ts.agent.Candidates) > 0:
		targetCandidates = append([]providers.FallbackCandidate(nil), ts.agent.Candidates...)
		targetModelName = strings.TrimSpace(ts.agent.Model)
		routeReason = "bypass_light_model_for_media"
	default:
		return nil
	}

	if len(targetCandidates) == 0 {
		return nil
	}

	targetModel := resolvedCandidateModel(targetCandidates, targetModelName)
	targetProvider := exec.activeProvider
	firstCandidate := targetCandidates[0]
	if provider, err := providerForFallbackCandidate(
		ts.agent,
		ts.agent.Provider,
		ts.agent.Candidates,
		firstCandidate,
	); err != nil {
		return err
	} else if provider != nil {
		targetProvider = provider
	}

	resolvedModelName := resolvedCandidateModelName(targetCandidates, targetModelName)
	if sameCandidateSet(exec.activeCandidates, targetCandidates) &&
		exec.activeModel == targetModel &&
		exec.llmModelName == resolvedModelName {
		return nil
	}

	exec.activeCandidates = targetCandidates
	exec.activeModel = targetModel
	exec.activeProvider = targetProvider
	exec.activeModelConfig = resolveActiveModelConfig(
		p.Cfg,
		ts.agent.Workspace,
		targetCandidates,
		targetModel,
		p.Cfg.Agents.Defaults.Provider,
	)
	exec.llmModelName = resolvedModelName
	exec.usedLight = false

	logger.InfoCF("agent", "Media turn routing selected model", map[string]any{
		"agent_id":       ts.agent.ID,
		"reason":         routeReason,
		"model":          exec.activeModel,
		"model_name":     exec.llmModelName,
		"candidates":     len(exec.activeCandidates),
		"messages_count": len(exec.callMessages),
	})

	return nil
}

const imageUnavailableNote = "[Image omitted: the image model is unavailable]"

// degradeFromImageModel moves a turn whose image-model call failed back to the
// base model, so one flaky vision backend does not abort the turn. Inline image
// data is dropped (path tags stay in the text) and image routing stays off for
// the rest of the turn. It returns the resolved base model name, or "" when the
// turn is not on the image model or there is no distinct base model to use.
func (p *Pipeline) degradeFromImageModel(ts *turnState, exec *turnExecution, cause error) string {
	if p == nil || ts == nil || ts.agent == nil || exec == nil || exec.imageRouteDisabled ||
		len(ts.agent.ImageCandidates) == 0 || len(ts.agent.Candidates) == 0 || ts.agent.Provider == nil ||
		!sameCandidateSet(exec.activeCandidates, ts.agent.ImageCandidates) ||
		sameCandidateSet(ts.agent.Candidates, ts.agent.ImageCandidates) {
		return ""
	}

	imageModel := exec.llmModelName
	baseModelName := p.useBaseCandidates(ts, exec)
	exec.imageRouteDisabled = true
	exec.messages = dropInlineMedia(exec.messages)
	exec.callMessages = dropInlineMedia(exec.callMessages)

	logger.WarnCF("agent", "Image model failed; falling back to base model without inline media", map[string]any{
		"agent_id":   ts.agent.ID,
		"from_model": imageModel,
		"to_model":   baseModelName,
		"error":      cause.Error(),
	})
	return baseModelName
}

// dropInlineMedia returns a copy of messages with inline media removed.
func dropInlineMedia(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, len(messages))
	for i, msg := range messages {
		if len(msg.Media) > 0 {
			msg.Media = nil
			if msg.Content == toolImageFollowUpContent {
				msg.Content = imageUnavailableNote
			}
		}
		out[i] = msg
	}
	return out
}
