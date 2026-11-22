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

	toolDefs := filterToolsByTurnProfile(ts.agent.Tools.ToProviderDefs(), ts.profile)
	provider := exec.activeProvider
	if provider == nil {
		provider = ts.agent.Provider
	}
	toolTokens := EstimateToolDefsTokens(toolDefs)
	threshold := summarizeThresholdTokens(ts.agent)
	promptTokensNow := func() int {
		total := toolTokens
		for _, m := range exec.messages {
			total += estimateMessageTokensForProvider(provider, m)
		}
		return total
	}

	var health CompactionHealthReporter
	if reporter, ok := p.ContextManager.(CompactionHealthReporter); ok {
		health = reporter
	}
	reportHealth := func(attempted, effective bool, promptTokens int) {
		if health == nil {
			return
		}
		health.RecordCompactionHealth(ctx, ts.sessionKey, CompactionHealthOutcome{
			AgentID:      ts.agent.ID,
			Attempted:    attempted,
			Effective:    effective,
			PromptTokens: promptTokens,
			Threshold:    threshold,
		})
	}

	attempted := false
	for pass := 0; pass < maxCompactionPasses; pass++ {
		promptTokens := promptTokensNow()
		// Assembly may already have evicted old items from the prompt. In that
		// case promptTokens can be below the threshold while the stored context
		// is still huge; compacting based only on the prompt would let the
		// eviction silently discard history without ever summarizing it.
		storedTokens := 0
		if reporter, ok := p.ContextManager.(ContextTokenReporter); ok {
			if n, err := reporter.ContextTokens(ctx, ts.sessionKey); err == nil {
				storedTokens = n
			}
		}
		if promptTokens < threshold && storedTokens < threshold {
			// Also clears stale cooldown/breaker state when a session drops back
			// under the threshold without a compaction.
			reportHealth(attempted, true, promptTokens)
			return
		}
		if storedTokens > promptTokens {
			logger.InfoCF("agent", "Stored context exceeds assembled prompt; compacting",
				map[string]any{
					"session_key":   ts.sessionKey,
					"prompt_tokens": promptTokens,
					"stored_tokens": storedTokens,
					"threshold":     threshold,
					"pass":          pass + 1,
				})
		}

		logger.InfoCF("agent", "Prompt reached summarize threshold, compacting before LLM call",
			map[string]any{
				"session_key":   ts.sessionKey,
				"prompt_tokens": promptTokens,
				"threshold":     threshold,
				"pass":          pass + 1,
			})
		if err := p.ContextManager.Compact(ctx, &CompactRequest{
			SessionKey:         ts.sessionKey,
			Reason:             ContextCompressReasonThreshold,
			Budget:             usableContextTokens(ts.agent),
			ActiveTurnMessages: activeTurnMessageCount(ts, exec),
		}); err != nil {
			switch {
			case errors.Is(err, ErrCompactionThrottled):
				logger.WarnCF("agent", "Threshold compaction skipped", map[string]any{
					"session_key": ts.sessionKey,
					"reason":      err.Error(),
				})
			case errors.Is(err, ErrCompactionInProgress):
				logger.DebugCF("agent", "Threshold compaction skipped; another summarization is in progress",
					map[string]any{"session_key": ts.sessionKey})
			case errors.Is(err, ErrNothingToCompact):
				logger.WarnCF("agent", "Threshold compaction made no progress", map[string]any{
					"session_key":   ts.sessionKey,
					"prompt_tokens": promptTokens,
					"threshold":     threshold,
				})
			default:
				logger.WarnCF("agent", "Threshold compaction failed", map[string]any{
					"session_key": ts.sessionKey,
					"error":       err.Error(),
				})
			}
			return
		}
		attempted = true

		rebuilt := p.reassembleActiveTurn(ctx, ts, exec, skillContextTriggerThresholdRebuild)
		if !rebuilt.assembled {
			// exec.history is stale and lacks this turn's messages: keep the prompt.
			return
		}
		exec.messages = rebuilt.build(rebuilt.stable)
		exec.currentTurnStart = len(exec.messages) - len(rebuilt.protected) - len(exec.loopWarnings)
	}
	if attempted {
		finalTokens := promptTokensNow()
		reportHealth(true, finalTokens < threshold, finalTokens)
		if finalTokens >= threshold {
			logger.WarnCF("agent", "Context still above summarize threshold after compaction", map[string]any{
				"session_key":   ts.sessionKey,
				"prompt_tokens": finalTokens,
				"threshold":     threshold,
				"passes":        maxCompactionPasses,
			})
		}
	}
}

// activeTurnMessageCount returns how many trailing messages belong to the turn
// in progress. Compaction uses it as a hard boundary: those messages are never
// folded into a summary, so the user's instructions cannot disappear mid-turn.
func activeTurnMessageCount(ts *turnState, exec *turnExecution) int {
	if ts != nil {
		if n := len(ts.persistedMessagesSnapshot()); n > 0 {
			return n
		}
	}
	if exec != nil && exec.currentTurnStart >= 0 && exec.currentTurnStart <= len(exec.messages) {
		return len(exec.messages) - exec.currentTurnStart
	}
	return 0
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
