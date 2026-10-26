// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// maxCompactionPasses bounds the compactions chained before one LLM call. One
// pass can leave the prompt over the threshold (earlier Turns summarized, the
// turn's own middle still to go); each further pass works on what is left.
const maxCompactionPasses = 3

// CompactBeforeLLM compacts the context synchronously when the prompt about to
// be sent has reached the summarize threshold, then rebuilds exec.messages from
// the compacted session so the turn carries on from there. It runs before every
// LLM call, so it covers both the start of a turn and the middle of a tool loop.
// Compaction is best effort: any failure leaves the prompt untouched, and the
// hard budget checks (proactive and context-error retry) remain the safety net.
func (p *Pipeline) CompactBeforeLLM(ctx context.Context, ts *turnState, exec *turnExecution) {
	if ts.opts.NoHistory || !ts.opts.EnableSummary || p.ContextManager == nil {
		return
	}

	toolTokens := EstimateToolDefsTokens(filterToolsByTurnProfile(ts.agent.Tools.ToProviderDefs(), ts.profile))
	threshold := summarizeThresholdTokens(ts.agent)
	for pass := 0; pass < maxCompactionPasses; pass++ {
		promptTokens := toolTokens
		for _, m := range exec.messages {
			promptTokens += EstimateMessageTokens(m)
		}
		if promptTokens < threshold {
			return
		}

		logger.InfoCF("agent", "Prompt reached summarize threshold, compacting before LLM call",
			map[string]any{
				"session_key":   ts.sessionKey,
				"prompt_tokens": promptTokens,
				"threshold":     threshold,
				"pass":          pass + 1,
			})
		if err := p.ContextManager.Compact(ctx, &CompactRequest{
			SessionKey: ts.sessionKey,
			Reason:     ContextCompressReasonThreshold,
			Budget:     ts.agent.ContextWindow,
		}); err != nil {
			if !errors.Is(err, ErrNothingToCompact) {
				logger.WarnCF("agent", "Threshold compaction failed", map[string]any{
					"session_key": ts.sessionKey,
					"error":       err.Error(),
				})
			}
			return
		}

		rebuilt := p.reassembleActiveTurn(ctx, ts, exec, skillContextTriggerThresholdRebuild)
		if !rebuilt.assembled {
			// exec.history is stale and lacks this turn's messages: keep the prompt.
			return
		}
		exec.messages = rebuilt.build(rebuilt.stable)
		exec.currentTurnStart = len(exec.messages) - len(rebuilt.protected) - len(exec.loopWarnings)
	}
}

// activeTurnPrompt is the prompt input re-read from the session after a
// compaction: the history compaction may still reshape (stable) and the active
// turn's own messages (protected), which are never dropped.
type activeTurnPrompt struct {
	assembled bool // false when the ContextManager could not be read back
	stable    []providers.Message
	protected []providers.Message
	// build renders system prompt + trimmedStable + protected + loop warnings.
	build func(trimmedStable []providers.Message) []providers.Message
}

// reassembleActiveTurn refreshes the restore point, re-reads history and
// summary through the ContextManager into exec, and returns what is needed to
// rebuild the prompt around the active turn.
func (p *Pipeline) reassembleActiveTurn(
	ctx context.Context,
	ts *turnState,
	exec *turnExecution,
	trigger string,
) activeTurnPrompt {
	ts.refreshRestorePointFromSession(ts.agent)
	assembled := true
	if asmResp, asmErr := p.ContextManager.Assemble(ctx, &AssembleRequest{
		SessionKey: ts.sessionKey,
		Budget:     ts.agent.ContextWindow,
		MaxTokens:  ts.agent.MaxTokens,
	}); asmErr == nil && asmResp != nil {
		exec.history = asmResp.History
		exec.summary = asmResp.Summary
	} else if asmErr != nil {
		warnSessionStoreError(ts.sessionKey, "assemble", asmErr)
		assembled = false
	}

	contextualSkills := ts.activeSkills
	if ts.agent.ContextBuilder != nil {
		contextualSkills = ts.agent.ContextBuilder.ResolveActiveSkillsForContext(ts.activeSkills)
	}
	ts.recordSkillContextSnapshot(trigger, contextualSkills)

	stable, protected := splitHistoryForActiveTurn(exec.history, ts.persistedMessagesSnapshot())
	mediaOpts := mediaResolveOptionsFor(p.Cfg)
	return activeTurnPrompt{
		assembled: assembled,
		stable:    stable,
		protected: protected,
		build: func(trimmedStable []providers.Message) []providers.Message {
			fullHistory := append(append([]providers.Message(nil), trimmedStable...), protected...)
			req := promptBuildRequestForTurn(ts, fullHistory, exec.summary, "", nil, p.Cfg)
			req.ActiveSkills = append([]string(nil), contextualSkills...)
			rebuilt := ts.agent.ContextBuilder.BuildMessagesFromPrompt(req)
			rebuilt = append(rebuilt, exec.loopWarnings...)
			return resolveMediaRefsWithOptions(
				rebuilt,
				p.MediaStore,
				mediaOpts,
				len(rebuilt)-len(protected)-len(exec.loopWarnings),
			)
		},
	}
}
