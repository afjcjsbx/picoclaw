// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"time"

	"github.com/afjcjsbx/picoclaw/pkg/channels"
	"github.com/afjcjsbx/picoclaw/pkg/providers"
	"github.com/afjcjsbx/picoclaw/pkg/utils"
)

// toolFeedbackBatch publishes the tool feedback for the tool calls of one LLM
// response. On channels that cannot edit messages every feedback becomes a new
// chat message, so the batch sends a single merged message per response and
// leaves out an explanation that repeats the previous one.
type toolFeedbackBatch struct {
	al      *AgentLoop
	ts      *turnState
	exec    *turnExecution
	compact bool
	sent    bool
}

func newToolFeedbackBatch(al *AgentLoop, ts *turnState, exec *turnExecution) *toolFeedbackBatch {
	return &toolFeedbackBatch{
		al:      al,
		ts:      ts,
		exec:    exec,
		compact: al.channelCannotEditMessages(ts.channel),
	}
}

// publish sends the feedback for exec.normalizedToolCalls[index], which runs as
// toolName with toolArgs after BeforeTool hooks. In compact mode the first call
// that reaches this point also lists the calls after it, and later calls of the
// same response publish nothing.
func (b *toolFeedbackBatch) publish(
	ctx context.Context,
	index int,
	toolCall providers.ToolCall,
	toolName string,
	toolArgs map[string]any,
	messages []providers.Message,
) {
	if !shouldPublishToolFeedback(b.al.cfg, b.ts) || b.ts.channel == "pico" {
		return
	}
	if b.compact && b.sent {
		return
	}

	maxArgsLen := b.al.cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength()
	explanation := toolFeedbackExplanationForToolCall(b.exec.response, toolCall, messages)
	var content string
	if b.compact {
		if b.ts.repeatsToolFeedbackExplanation(explanation) {
			explanation = ""
		}
		calls := []utils.ToolFeedbackCall{{
			Name:        toolName,
			ArgsPreview: toolFeedbackArgsPreview(toolArgs, maxArgsLen),
		}}
		for _, next := range b.exec.normalizedToolCalls[index+1:] {
			calls = append(calls, utils.ToolFeedbackCall{
				Name:        next.Name,
				ArgsPreview: toolFeedbackArgsPreview(next.Arguments, maxArgsLen),
			})
		}
		content = utils.FormatMergedToolFeedbackMessage(calls, explanation)
		b.sent = true
	} else {
		content = utils.FormatToolFeedbackMessage(
			toolName,
			explanation,
			toolFeedbackArgsPreview(toolArgs, maxArgsLen),
		)
	}

	fbCtx, fbCancel := context.WithTimeout(ctx, 3*time.Second)
	defer fbCancel()
	_ = b.al.bus.PublishOutbound(fbCtx, outboundMessageForTurnWithOptions(
		b.ts,
		content,
		outboundTurnMessageOptions{kind: messageKindToolFeedback},
	))
}

// channelCannotEditMessages reports whether channel is a known channel without
// message editing, where every tool feedback shows up as a new chat message.
func (al *AgentLoop) channelCannotEditMessages(channel string) bool {
	if al.channelManager == nil || channel == "" {
		return false
	}
	ch, ok := al.channelManager.GetChannel(channel)
	if !ok || ch == nil {
		return false
	}
	_, canEdit := ch.(channels.MessageEditor)
	return !canEdit
}
