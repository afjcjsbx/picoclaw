// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func (al *AgentLoop) runTurn(ctx context.Context, ts *turnState, pipeline *Pipeline) (turnResult, error) {
	if ts != nil && ts.agent != nil {
		modelMu := ts.agent.modelStateMutex()
		modelMu.RLock()
		defer modelMu.RUnlock()
	}
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	ts.setTurnCancel(turnCancel)

	// Steering and graceful interrupts may extend a turn past MaxIterations so
	// the user can redirect a long task. Keep that extension bounded: without
	// an absolute cap a steering message can keep a degenerate loop alive
	// forever.
	iterationHardCap := steeringIterationHardCap(ts.agent.MaxIterations)

	// Inject turnState and AgentLoop into context so tools (e.g. spawn) can retrieve them.
	turnCtx = withTurnState(turnCtx, ts)
	turnCtx = WithAgentLoop(turnCtx, al)

	al.registerActiveTurn(ts)
	defer al.clearActiveTurn(ts)

	if al.takePendingStop(ts.sessionKey) {
		_ = ts.requestHardAbort()
	}

	turnStatus := TurnEndStatusCompleted
	defer func() {
		// A SubTurn never ends before its async children, whatever the exit path
		// (iteration cap, loop guard, hook abort, ...). The direct-answer path has
		// already fed their results to the model; here nobody can read them any
		// more, so they are dropped.
		if ts.parentTurnState != nil {
			if dropped := ts.joinChildren(turnCtx, func(s string) string { return s }); len(dropped) > 0 {
				logger.WarnCF("agent", "SubTurn ended with unread child results", map[string]any{
					"agent_id": ts.agentID,
					"turn_id":  ts.turnID,
					"dropped":  len(dropped),
				})
			}
		}

		attemptedSkills := ts.attemptedSkillsSnapshot()
		skillContextSnapshots := ts.skillContextSnapshotsSnapshot()
		finalSuccessfulPath := []string(nil)
		if turnStatus == TurnEndStatusCompleted {
			if latest := ts.latestSkillContextSnapshot(); len(latest) > 0 {
				finalSuccessfulPath = latest
			} else {
				finalSuccessfulPath = append([]string(nil), attemptedSkills...)
			}
		}
		al.emitEvent(
			runtimeevents.KindAgentTurnEnd,
			ts.eventMeta("runTurn", "turn.end"),
			TurnEndPayload{
				Status:                turnStatus,
				Workspace:             ts.workspace,
				Iterations:            ts.currentIteration(),
				Duration:              time.Since(ts.startedAt),
				FinalContentLen:       ts.finalContentLen(),
				UserMessage:           ts.userMessage,
				FinalContent:          ts.finalContentSnapshot(),
				ActiveSkills:          append([]string(nil), ts.activeSkills...),
				AttemptedSkills:       attemptedSkills,
				FinalSuccessfulPath:   finalSuccessfulPath,
				SkillContextSnapshots: skillContextSnapshots,
				ToolKinds:             ts.toolKindsSnapshot(),
				ToolExecutions:        ts.toolExecutionsSnapshot(),
			},
		)

		// Mark the turn as finished so that late SubTurn results are reported as
		// orphans and direct child turns observe that the parent ended. Hard aborts
		// already finished the turn (directly or through the parent cascade).
		if !ts.isFinished.Load() {
			ts.Finish(ts.hardAbortRequested())
		}
	}()

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	al.emitEvent(
		runtimeevents.KindAgentTurnStart,
		ts.eventMeta("runTurn", "turn.start"),
		TurnStartPayload{
			UserMessage: ts.userMessage,
			MediaCount:  len(ts.media),
		},
	)

	// Discovery tools stay unlocked for a configured number of conversational
	// turns. Tick once per turn, at the start, so a tool discovered during a
	// turn remains callable for that entire turn (including later tool-loop
	// iterations). Ticking per iteration would decrement it before the model
	// could use it, making ttl=1 unusable.
	if ts.agent != nil && ts.agent.Tools != nil {
		ts.agent.Tools.TickTTL()
	}

	// SetupTurn extracts the one-time initialization phase.
	exec, err := pipeline.SetupTurn(turnCtx, ts)
	if err != nil {
		return turnResult{}, err
	}
	defer exec.closeOwnedProviders()

	// Convenience references to exec fields used throughout the turn loop.
	messages := exec.messages
	pendingMessages := exec.pendingMessages
	mediaOpts := mediaResolveOptionsFor(pipeline.Cfg)
	finalContent := exec.finalContent

	// The graceful terminal iteration runs without tools and only once, so it
	// may use the iteration at the cap itself.
	for ts.currentIteration() < ts.agent.MaxIterations ||
		(ts.currentIteration() < iterationHardCap && len(exec.pendingMessages) > 0) ||
		(ts.currentIteration() <= iterationHardCap && func() bool {
			graceful, _ := ts.gracefulInterruptRequested()
			return graceful
		}()) {
		if ts.hardAbortRequested() {
			turnStatus = TurnEndStatusAborted
			return al.abortTurn(ts)
		}

		iteration := ts.currentIteration() + 1
		ts.setIteration(iteration)
		ts.setPhase(TurnPhaseRunning)

		if iteration > 1 {
			// For subsequent iterations, read from exec.pendingMessages which
			// is where ExecuteTools (or initial poll) deposits steering.
			// We do NOT call dequeueSteeringMessagesForScope here because
			// steering was already consumed from al.steering by ExecuteTools.
			//
			// Assign instead of appending: after every LLM call the local
			// pendingMessages is already synced from exec.pendingMessages
			// (see the CallLLM return handling below), so appending here would
			// duplicate messages that arrived via the post-response poll.
			if len(exec.pendingMessages) > 0 {
				pendingMessages = exec.pendingMessages
				exec.pendingMessages = nil
			}
		} else if !ts.opts.SkipInitialSteeringPoll {
			if steerMsgs := al.dequeueSteeringMessagesForScopeWithFallback(ts.sessionKey); len(steerMsgs) > 0 {
				pendingMessages = append(pendingMessages, steerMsgs...)
			}
		}

		// Check if parent turn has ended (SubTurn support from HEAD)
		if ts.parentTurnState != nil && ts.IsParentEnded() {
			if !ts.critical {
				logger.InfoCF("agent", "Parent turn ended, non-critical SubTurn exiting gracefully", map[string]any{
					"agent_id":  ts.agentID,
					"iteration": iteration,
					"turn_id":   ts.turnID,
				})
				break
			}
			logger.InfoCF("agent", "Parent turn ended, critical SubTurn continues running", map[string]any{
				"agent_id":  ts.agentID,
				"iteration": iteration,
				"turn_id":   ts.turnID,
			})
		}

		// Poll for pending SubTurn results (from HEAD)
		if ts.pendingResults != nil {
			select {
			case result, ok := <-ts.pendingResults:
				if ok && result != nil && result.ForLLM != "" {
					content := al.cfg.FilterSensitiveData(result.ForLLM)
					msg := subTurnResultPromptMessage(content)
					pendingMessages = append(pendingMessages, msg)
				}
			default:
				// No results available
			}
		}

		// Inject pending steering messages
		if len(pendingMessages) > 0 {
			resolvedPending := resolveMediaRefsWithOptions(pendingMessages, al.mediaStore, mediaOpts, 0)
			totalContentLen := 0
			for i, pm := range pendingMessages {
				messages = append(messages, resolvedPending[i])
				totalContentLen += len(pm.Content)
				if !ts.opts.NoHistory {
					warnSessionStoreError(ts.sessionKey, "add full message",
						ts.agent.Sessions.AddFullMessage(ctx, ts.sessionKey, pm))
					ts.recordPersistedMessage(pm)
					ts.ingestMessage(turnCtx, al, pm)
				}
				logger.InfoCF("agent", "Injected steering message into context",
					map[string]any{
						"agent_id":    ts.agent.ID,
						"iteration":   iteration,
						"content_len": len(pm.Content),
						"media_count": len(pm.Media),
					})
			}
			al.emitEvent(
				runtimeevents.KindAgentSteeringInjected,
				ts.eventMeta("runTurn", "turn.steering.injected"),
				SteeringInjectedPayload{
					Count:           len(pendingMessages),
					TotalContentLen: totalContentLen,
				},
			)
			// Clear exec.pendingMessages after injection so InitialSteeringMessages
			// are not re-injected on subsequent iterations (Issue 2 fix).
			exec.pendingMessages = nil
		}
		// Always sync messages into exec.messages so CallLLM sees the updated state
		exec.messages = messages

		logger.DebugCF("agent", "LLM iteration",
			map[string]any{
				"agent_id":  ts.agent.ID,
				"iteration": iteration,
				"max":       ts.agent.MaxIterations,
			})

		// Execute LLM call via Pipeline
		ts.setPhase(TurnPhaseRunning)
		ctrl, callErr := pipeline.CallLLM(ctx, turnCtx, ts, exec, iteration)
		if callErr != nil {
			turnStatus = TurnEndStatusError
			return turnResult{}, callErr
		}
		messages = exec.messages
		pendingMessages = exec.pendingMessages
		finalContent = exec.finalContent

		switch ctrl {
		case ControlContinue:
			continue
		case ControlBreak:
			// Hard abort: delegate to abortTurn (sets TurnEndStatusAborted)
			if exec.abortedByHardAbort {
				turnStatus = TurnEndStatusAborted
				return al.abortTurn(ts)
			}
			// Hook abort (HookActionAbortTurn): sets TurnEndStatusError, returns error
			if exec.abortedByHook {
				turnStatus = TurnEndStatusError
				return turnResult{}, fmt.Errorf("hook requested turn abort")
			}
			// Ensure an empty response falls back to a user-facing message,
			// with a dedicated hint when the model was cut off by max_tokens.
			if finalContent == "" {
				finalContent = emptyResponseFallback(ts)
			}
			result, finalizeErr := pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
			if finalizeErr != nil {
				turnStatus = TurnEndStatusError
			}
			return result, finalizeErr
		case ControlToolLoop:
			// Execute tools via Pipeline
			toolCtrl := pipeline.ExecuteTools(ctx, turnCtx, ts, exec, iteration)
			switch toolCtrl {
			case ToolControlContinue:
				// Re-read exec.messages since ExecuteTools may have updated it
				// (added tool results/skipped messages) before returning ControlContinue
				messages = exec.messages
				continue
			case ToolControlBreak:
				// Hard abort: delegate to abortTurn (sets TurnEndStatusAborted)
				if exec.abortedByHardAbort {
					turnStatus = TurnEndStatusAborted
					return al.abortTurn(ts)
				}
				// Hook abort (HookActionAbortTurn): sets TurnEndStatusError, returns error
				if exec.abortedByHook {
					turnStatus = TurnEndStatusError
					return turnResult{}, fmt.Errorf("hook requested turn abort")
				}
				// ExecuteTools may provide a terminal response (for example when
				// repeated tool-call loop detection stops the turn).
				if exec.finalContent != "" {
					finalContent = exec.finalContent
				}
				// ExecuteTools returned ControlBreak:
				// - allResponsesHandled=true: finalize without DefaultResponse (exec.finalContent empty)
				// - allResponsesHandled=false: coordinator applies DefaultResponse before finalize
				if exec.allResponsesHandled {
					finalContent = ""
				}
				result, finalizeErr := pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
				if finalizeErr != nil {
					turnStatus = TurnEndStatusError
				}
				return result, finalizeErr
			}
		}
	}

	// The hard cap can end the turn with steering already pulled off the
	// queue. Hand it back so the post-turn continuation still delivers it.
	if ts.parentTurnState == nil {
		al.requeueSteeringMessagesForScope(ts.sessionKey, exec.pendingMessages)
		exec.pendingMessages = nil
	}

	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	if finalContent == "" {
		if ts.currentIteration() >= ts.agent.MaxIterations && ts.agent.MaxIterations > 0 {
			finalContent = toolLimitResponse
		} else {
			finalContent = emptyResponseFallback(ts)
		}
	}

	// Check hard abort before finalizing (may have been set during tool execution)
	if ts.hardAbortRequested() {
		turnStatus = TurnEndStatusAborted
		return al.abortTurn(ts)
	}

	result, err := pipeline.Finalize(ctx, turnCtx, ts, exec, turnStatus, finalContent)
	if err != nil {
		turnStatus = TurnEndStatusError
	}
	return result, err
}

func (al *AgentLoop) abortTurn(ts *turnState) (turnResult, error) {
	ts.setPhase(TurnPhaseAborted)
	if !ts.opts.NoHistory {
		if err := ts.restoreSession(ts.agent); err != nil {
			al.emitEvent(
				runtimeevents.KindAgentError,
				ts.eventMeta("abortTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_restore",
					Message: err.Error(),
				},
			)
			return turnResult{}, err
		}
	}
	return turnResult{status: TurnEndStatusAborted}, nil
}

func (al *AgentLoop) selectCandidates(
	agent *AgentInstance,
	userMsg string,
	history []providers.Message,
	hasMedia bool,
) (candidates []providers.FallbackCandidate, model string, usedLight bool) {
	if agent.Router == nil || len(agent.LightCandidates) == 0 {
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	_, usedLight, score := agent.Router.SelectModelWithMedia(userMsg, history, agent.Model, hasMedia)
	if !usedLight {
		logger.DebugCF("agent", "Model routing: primary model selected",
			map[string]any{
				"agent_id":  agent.ID,
				"score":     score,
				"threshold": agent.Router.Threshold(),
			})
		return agent.Candidates, resolvedCandidateModel(agent.Candidates, agent.Model), false
	}

	logger.InfoCF("agent", "Model routing: light model selected",
		map[string]any{
			"agent_id":    agent.ID,
			"light_model": agent.Router.LightModel(),
			"score":       score,
			"threshold":   agent.Router.Threshold(),
		})
	return agent.LightCandidates, resolvedCandidateModel(agent.LightCandidates, agent.Router.LightModel()), true
}

func (al *AgentLoop) resolveContextManager() ContextManager {
	name := al.cfg.Agents.Defaults.ContextManager
	if name == "" || name == "legacy" {
		return &legacyContextManager{al: al}
	}
	factory, ok := lookupContextManager(name)
	if !ok {
		logger.WarnCF("agent", "Unknown context manager, falling back to legacy", map[string]any{
			"name": name,
		})
		return &legacyContextManager{al: al}
	}
	cm, err := factory(al.cfg.Agents.Defaults.ContextManagerConfig, al)
	if err != nil {
		logger.WarnCF("agent", "Failed to create context manager, falling back to legacy", map[string]any{
			"name":  name,
			"error": err.Error(),
		})
		return &legacyContextManager{al: al}
	}
	return cm
}

// steeringIterationHardCap bounds how far steering/graceful interrupts can
// extend a turn beyond MaxIterations. The extra room is intentionally generous
// for normal redirects but finite, so a model that keeps looping cannot burn
// tokens indefinitely.
func steeringIterationHardCap(maxIterations int) int {
	if maxIterations <= 0 {
		return 0
	}
	hardCap := maxIterations * 3
	if floor := maxIterations + 20; floor > hardCap {
		hardCap = floor
	}
	return hardCap
}
