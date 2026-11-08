package seahorse

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

// CompactInput controls compaction behavior.
type CompactInput struct {
	Budget *int // Token budget override
	Force  bool // Force compaction even if below threshold
	// ProtectTailMessages is the number of newest messages that must stay
	// verbatim. The agent sets it to the active turn's persisted message count
	// so compaction can never fold the instructions being worked on.
	ProtectTailMessages int
}

// CompactResult describes what was compacted.
type CompactResult struct {
	SummariesCreated   []string `json:"summariesCreated"`
	TokensSaved        int      `json:"tokensSaved"`
	LeafSummaries      int      `json:"leafSummaries"`
	CondensedSummaries int      `json:"condensedSummaries"`
}

// NeedsCompaction returns true if context tokens >= ContextThreshold × contextWindow.
func (e *CompactionEngine) NeedsCompaction(
	ctx context.Context,
	convID int64,
	contextWindow int,
) (bool, error) {
	tokens, err := e.store.GetContextTokenCount(ctx, convID)
	if err != nil {
		return false, fmt.Errorf("get token count: %w", err)
	}
	threshold := int(float64(contextWindow) * ContextThreshold)
	return tokens >= threshold, nil
}

// condensedMinFanout returns the minimum number of summaries required for a
// condensed pass. Forced passes may condense smaller runs.
func condensedMinFanout(forced bool) int {
	if forced {
		return CondensedMinFanoutHard
	}
	return CondensedMinFanout
}

// Close cancels the shutdown context, stopping async goroutines.
func (e *CompactionEngine) Close() {
	if e.shutdownCancel != nil {
		e.shutdownCancel()
	}
}

// Compact runs leaf compaction (sync) and optionally condensed compaction.
func (e *CompactionEngine) Compact(
	ctx context.Context,
	convID int64,
	input CompactInput,
) (*CompactResult, error) {
	result := &CompactResult{}

	// Phase 1: leaf compaction (synchronous, every turn)
	summaryID, err := e.compactLeafProtected(ctx, convID, "", input.ProtectTailMessages, false)
	if err != nil {
		return nil, fmt.Errorf("compact leaf: %w", err)
	}
	if summaryID != nil {
		result.SummariesCreated = append(result.SummariesCreated, *summaryID)
		result.LeafSummaries++
		logger.InfoCF("seahorse", "compact: leaf", map[string]any{
			"conv_id":    convID,
			"summary_id": *summaryID,
		})
	}

	// Phase 2: condensed compaction if over threshold
	tokensBefore, _ := e.store.GetContextTokenCount(ctx, convID)
	var budget int
	if input.Budget != nil {
		budget = *input.Budget
		if budget == 0 {
			logger.ErrorCF(
				"seahorse",
				"Compact: budget is 0, this should not happen",
				map[string]any{
					"conv_id": convID,
				},
			)
		}
	} else {
		// No target means no automatic condensed pass.
		budget = tokensBefore
	}

	if input.Force || (tokensBefore > budget && budget > 0) {
		// Launch async condensed compaction with dedup. The goroutine gets the
		// same target budget so it stops as soon as the stored context fits.
		// A forced pass deliberately ignores the target and condenses all
		// eligible candidates.
		loopBudget := budget
		if input.Force {
			loopBudget = 0
		}
		if _, loaded := e.condensing.LoadOrStore(convID, struct{}{}); !loaded {
			go func() {
				defer e.condensing.Delete(convID)
				e.runCondensedLoop(e.shutdownCtx, convID, "", loopBudget)
			}()
		}
	}

	tokensAfter, _ := e.store.GetContextTokenCount(ctx, convID)
	if tokensAfter < tokensBefore {
		result.TokensSaved = tokensBefore - tokensAfter
	}

	return result, nil
}

// CompactNow runs one leaf pass followed by the condensed loop, all synchronously,
// so the caller can report what actually happened. Unlike Compact it ignores
// thresholds beyond the per-pass minimums. protectTailMessages keeps the active
// turn verbatim. instructions (may be empty) tells the summarizer which details
// to prioritize.
func (e *CompactionEngine) CompactNow(
	ctx context.Context,
	convID int64,
	instructions string,
	protectTailMessages ...int,
) (*CompactResult, error) {
	protect := 0
	if len(protectTailMessages) > 0 {
		protect = protectTailMessages[0]
	}
	result := &CompactResult{}
	tokensBefore, _ := e.store.GetContextTokenCount(ctx, convID)

	summaryID, err := e.compactLeafProtected(ctx, convID, instructions, protect, false)
	if err != nil {
		return nil, fmt.Errorf("compact leaf: %w", err)
	}
	if summaryID != nil {
		result.SummariesCreated = append(result.SummariesCreated, *summaryID)
		result.LeafSummaries++
	}

	// Share the dedup map with the async path so two condensed loops never overlap.
	if _, loaded := e.condensing.LoadOrStore(convID, struct{}{}); !loaded {
		defer e.condensing.Delete(convID)
		result.CondensedSummaries = e.runCondensedLoop(ctx, convID, instructions, 0)
	}

	if tokensAfter, _ := e.store.GetContextTokenCount(ctx, convID); tokensAfter < tokensBefore {
		result.TokensSaved = tokensBefore - tokensAfter
	}
	return result, nil
}

// CompactUntilUnder aggressively compacts until context is under budget.
func (e *CompactionEngine) CompactUntilUnder(
	ctx context.Context,
	convID int64,
	budget int,
	protectTailMessages ...int,
) (*CompactResult, error) {
	protect := 0
	if len(protectTailMessages) > 0 {
		protect = protectTailMessages[0]
	}
	result := &CompactResult{}
	prevTokens := 0
	logger.InfoCF(
		"seahorse",
		"compact_until_under: start",
		map[string]any{"conv_id": convID, "budget": budget},
	)

	for iter := 0; iter < MaxCompactIterations; iter++ {
		tokens, err := e.store.GetContextTokenCount(ctx, convID)
		if err != nil {
			return result, fmt.Errorf("get tokens: %w", err)
		}
		if tokens <= budget {
			logger.InfoCF("seahorse", "compact_until_under: done", map[string]any{
				"conv_id":   convID,
				"budget":    budget,
				"tokens":    tokens,
				"leaf":      result.LeafSummaries,
				"condensed": result.CondensedSummaries,
			})
			return result, nil
		}

		// Try leaf first. Force bypasses the fresh tail but never the
		// active-turn boundary.
		summaryID, err := e.compactLeafProtected(ctx, convID, "", protect, true)
		if err != nil {
			return result, err
		}
		if summaryID != nil {
			result.SummariesCreated = append(result.SummariesCreated, *summaryID)
			result.LeafSummaries++
			logger.InfoCF("seahorse", "compact_until_under: leaf", map[string]any{
				"conv_id":    convID,
				"summary_id": *summaryID,
			})
			continue
		}

		// Try condensed with forced fanout
		condensedID, err := e.compactCondensed(ctx, convID, "", true)
		if err != nil {
			return result, err
		}
		if condensedID != nil {
			result.SummariesCreated = append(result.SummariesCreated, *condensedID)
			result.CondensedSummaries++
			logger.InfoCF("seahorse", "compact_until_under: condensed", map[string]any{
				"conv_id":    convID,
				"summary_id": *condensedID,
			})
			continue
		}

		// No progress
		newTokens, _ := e.store.GetContextTokenCount(ctx, convID)
		if newTokens >= prevTokens {
			logger.WarnCF("seahorse", "compact_until_under: no progress", map[string]any{
				"conv_id": convID,
				"tokens":  newTokens,
			})
			return result, nil
		}
		prevTokens = newTokens
	}

	// Safety cap exceeded — see MaxCompactIterations doc for rationale.
	logger.WarnCF("seahorse", "compact_until_under: exceeded max iterations", map[string]any{
		"conv_id":    convID,
		"budget":     budget,
		"iterations": MaxCompactIterations,
		"tokens":     prevTokens,
	})
	return result, nil
}

// compactLeaf compresses the oldest contiguous message chunk into a leaf
// summary. It is the backwards-compatible entry point without active-turn
// protection; force bypasses FreshTailCount.
func (e *CompactionEngine) compactLeaf(
	ctx context.Context,
	convID int64,
	instructions string,
	force ...bool,
) (*string, error) {
	useForce := len(force) > 0 && force[0]
	return e.compactLeafProtected(ctx, convID, instructions, 0, useForce)
}

// compactLeafProtected is compactLeaf with an explicit active-turn boundary.
// protectTailMessages always wins over force: the newest messages of the
// active turn can never be summarized.
func (e *CompactionEngine) compactLeafProtected(
	ctx context.Context,
	convID int64,
	instructions string,
	protectTailMessages int,
	force bool,
) (*string, error) {
	items, err := e.store.GetContextItems(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Find oldest contiguous message chunk outside fresh tail
	msgCount := 0
	msgTokens := 0
	for _, item := range items {
		if item.ItemType == "message" {
			msgCount++
			msgTokens += item.TokenCount
		}
	}

	// Trigger if either message count or token threshold is met
	if msgCount < LeafMinFanout && msgTokens < LeafChunkTokens {
		return nil, nil
	}

	// Calculate the protected tail boundary. Force bypasses the normal fresh
	// tail, but the active-turn boundary is always enforced.
	tailStartIdx := compactionTailStart(items, protectTailMessages, force)

	// Find oldest contiguous message chunk, accumulating up to LeafChunkTokens
	var chunk []ContextItem
	chunkStart := -1
	chunkEnd := -1
	accumTokens := 0
	for i := 0; i < tailStartIdx; i++ {
		if items[i].ItemType == "message" {
			if chunkStart == -1 {
				chunkStart = i
			}
			chunkEnd = i
			accumTokens += items[i].TokenCount
			// Stop accumulating once we reach the token budget
			if accumTokens >= LeafChunkTokens {
				break
			}
		} else {
			// Non-message breaks the chunk
			if chunkStart != -1 && (chunkEnd-chunkStart+1) >= LeafMinFanout {
				break
			}
			chunkStart = -1
			chunkEnd = -1
			accumTokens = 0
		}
	}

	if chunkStart == -1 || (chunkEnd-chunkStart+1) < LeafMinFanout {
		return nil, nil
	}

	chunk = items[chunkStart : chunkEnd+1]

	// Collect messages for the chunk
	var messages []Message
	for _, item := range chunk {
		msg, innerErr := e.store.GetMessageByID(ctx, item.MessageID)
		if innerErr != nil {
			return nil, innerErr
		}
		messages = append(messages, *msg)
	}

	// Get prior summaries for context
	priorSummary := ""
	priorCount := 0
	for i := chunkStart - 1; i >= 0 && priorCount < 2; i-- {
		if items[i].ItemType == "summary" {
			sum, innerErr2 := e.store.GetSummary(ctx, items[i].SummaryID)
			if innerErr2 == nil {
				priorSummary = sum.Content + "\n" + priorSummary
				priorCount++
			}
		}
	}

	// Generate summary
	content, err := e.generateLeafSummary(ctx, messages, priorSummary, instructions)
	if err != nil {
		return nil, err
	}

	// Create the summary and rewrite the context atomically. The LLM call above
	// is intentionally outside the transaction; ApplyLeafSummary verifies that
	// the candidate set is still current and returns ErrStaleCandidates rather
	// than creating an orphan.
	tokenCount := tokenizer.EstimateMessageTokens(providers.Message{Content: content})

	var earliestAt, latestAt *time.Time
	if len(messages) > 0 {
		earliestAt = &messages[0].CreatedAt
		latestAt = &messages[len(messages)-1].CreatedAt
	}
	version, err := e.store.GetConversationVersion(ctx, convID)
	if err != nil {
		return nil, err
	}

	msgIDs := make([]int64, len(messages))
	for i, m := range messages {
		msgIDs[i] = m.ID
	}

	unlock := e.lockConversation(convID)
	defer unlock()
	summary, err := e.store.ApplyLeafSummary(ctx, ApplyLeafInput{
		ConversationID:  convID,
		ExpectedVersion: version,
		StartOrdinal:    chunk[0].Ordinal,
		EndOrdinal:      chunk[len(chunk)-1].Ordinal,
		MessageIDs:      msgIDs,
		Summary: CreateSummaryInput{
			ConversationID:      convID,
			Kind:                SummaryKindLeaf,
			Depth:               0,
			Content:             content,
			TokenCount:          tokenCount,
			EarliestAt:          earliestAt,
			LatestAt:            latestAt,
			SourceMessageTokens: sumMessageTokens(messages),
		},
	})
	if err != nil {
		if errors.Is(err, ErrStaleCandidates) {
			logger.InfoCF("seahorse", "compact: leaf candidates changed, skipping", map[string]any{
				"conv_id": convID,
			})
			return nil, nil
		}
		return nil, err
	}

	return &summary.SummaryID, nil
}

// compactionTailStart returns the first context item index that must not be
// summarized. Without force, the normal FreshTailCount applies. When
// protectTailMessages is positive, it extends protection back until that many
// message items are covered, which keeps the active turn whole.
func compactionTailStart(items []ContextItem, protectTailMessages int, force bool) int {
	start := len(items)
	if !force {
		start = len(items) - FreshTailCount
		if start < 0 {
			start = 0
		}
	}
	if protectTailMessages <= 0 {
		return start
	}

	seen := 0
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].ItemType != "message" {
			continue
		}
		seen++
		if seen >= protectTailMessages {
			if i < start {
				return i
			}
			return start
		}
	}
	// Fewer messages than requested: protect the whole context.
	return 0
}

// compactCondensed compresses multiple summaries into one higher-level summary.
func (e *CompactionEngine) compactCondensed(
	ctx context.Context,
	convID int64,
	instructions string,
	forced bool,
) (*string, error) {
	minFanout := condensedMinFanout(forced)

	// Try ordinal-aware selection first (respects consecutive ordering)
	var candidates []Summary

	depths, err := e.store.GetDistinctDepthsInContext(ctx, convID, 0)
	if err != nil {
		return nil, err
	}
	for _, depth := range depths {
		var chunkAtDepth []Summary
		var err2 error
		chunkAtDepth, err2 = e.selectOldestChunkAtDepth(ctx, convID, depth, forced)
		if err2 != nil {
			continue
		}
		if len(chunkAtDepth) > 0 {
			candidates = chunkAtDepth
			break
		}
	}

	// Fallback to depth-grouping selection
	if len(candidates) == 0 {
		candidates, err = e.selectShallowestCondensationCandidate(ctx, convID, forced)
		if err != nil {
			return nil, err
		}
	}
	if len(candidates) == 0 || len(candidates) < minFanout {
		return nil, nil
	}

	version, err := e.store.GetConversationVersion(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Generate condensed summary
	content, err := e.generateCondensedSummary(ctx, candidates, instructions)
	if err != nil {
		return nil, err
	}

	// Merge metadata
	maxDepth := 0
	descendantCount := 0
	descendantTokenCount := 0
	sourceMessageTokens := 0
	var earliestAt, latestAt *time.Time

	parentIDs := make([]string, len(candidates))
	inputTokens := 0
	for i, c := range candidates {
		parentIDs[i] = c.SummaryID
		inputTokens += c.TokenCount
		if c.Depth > maxDepth {
			maxDepth = c.Depth
		}
		descendantCount += c.DescendantCount + 1
		descendantTokenCount += c.TokenCount + c.DescendantTokenCount
		sourceMessageTokens += c.SourceMessageTokenCount
		if c.EarliestAt != nil {
			if earliestAt == nil || c.EarliestAt.Before(*earliestAt) {
				earliestAt = c.EarliestAt
			}
		}
		if c.LatestAt != nil {
			if latestAt == nil || c.LatestAt.After(*latestAt) {
				latestAt = c.LatestAt
			}
		}
	}

	tokenCount := tokenizer.EstimateMessageTokens(providers.Message{Content: content})
	// Condensation must reduce the context. A model that returns a response as
	// large as its input would only add a summary level and waste an LLM call.
	if inputTokens > 0 && tokenCount >= inputTokens {
		logger.InfoCF(
			"seahorse",
			"condensed: response is not smaller than input, skipping",
			map[string]any{
				"conv_id":       convID,
				"input_tokens":  inputTokens,
				"output_tokens": tokenCount,
			},
		)
		return nil, nil
	}

	// Find the ordinal at which the condensed summary belongs. ApplyCondensed
	// Summary re-verifies that every candidate is still present before it
	// creates any row, so this read is only a hint, not a lock.
	items, err := e.store.GetContextItems(ctx, convID)
	if err != nil {
		return nil, err
	}
	candidateSet := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		candidateSet[c.SummaryID] = true
	}
	startOrd := -1
	for _, item := range items {
		if item.ItemType == "summary" && candidateSet[item.SummaryID] {
			if startOrd == -1 || item.Ordinal < startOrd {
				startOrd = item.Ordinal
			}
		}
	}
	if startOrd == -1 {
		return nil, nil
	}

	candidateIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		candidateIDs = append(candidateIDs, c.SummaryID)
	}

	unlock := e.lockConversation(convID)
	defer unlock()
	summary, err := e.store.ApplyCondensedSummary(ctx, ApplyCondensedInput{
		ConversationID:      convID,
		ExpectedVersion:     version,
		CandidateSummaryIDs: candidateIDs,
		InsertBeforeOrdinal: startOrd,
		Summary: CreateSummaryInput{
			ConversationID:       convID,
			Kind:                 SummaryKindCondensed,
			Depth:                maxDepth + 1,
			Content:              content,
			TokenCount:           tokenCount,
			EarliestAt:           earliestAt,
			LatestAt:             latestAt,
			DescendantCount:      descendantCount,
			DescendantTokenCount: descendantTokenCount,
			SourceMessageTokens:  sourceMessageTokens,
			ParentIDs:            parentIDs,
		},
	})
	if err != nil {
		if errors.Is(err, ErrStaleCandidates) {
			logger.InfoCF("seahorse", "condensed: candidates changed, skipping", map[string]any{
				"conv_id": convID,
			})
			return nil, nil
		}
		return nil, err
	}

	return &summary.SummaryID, nil
}

// selectShallowestCondensationCandidate finds the shallowest consecutive summary group.
func (e *CompactionEngine) selectShallowestCondensationCandidate(
	ctx context.Context, convID int64, forced bool,
) ([]Summary, error) {
	items, err := e.store.GetContextItems(ctx, convID)
	if err != nil {
		return nil, err
	}

	// Group by depth, find consecutive runs
	tailStartIdx := len(items) - FreshTailCount
	if tailStartIdx < 0 {
		tailStartIdx = 0
	}

	minFanout := condensedMinFanout(forced)

	// Track depth groups
	depthGroups := make(map[int][]ContextItem)
	for i := 0; i < tailStartIdx; i++ {
		item := items[i]
		if item.ItemType != "summary" {
			continue
		}
		sum, err := e.store.GetSummary(ctx, item.SummaryID)
		if err != nil {
			continue
		}
		if sum.TokenCount > LeafChunkTokens {
			continue
		}
		depthGroups[sum.Depth] = append(depthGroups[sum.Depth], item)
	}

	// Find shallowest depth with enough candidates
	// Collect all depths and sort to handle non-consecutive depths
	var depths []int
	for depth := range depthGroups {
		depths = append(depths, depth)
	}
	sort.Ints(depths)

	for _, depth := range depths {
		group := depthGroups[depth]
		if len(group) >= minFanout {
			// Load summaries
			var result []Summary
			for _, item := range group[:minFanout] {
				sum, err := e.store.GetSummary(ctx, item.SummaryID)
				if err != nil {
					continue
				}
				result = append(result, *sum)
			}
			return result, nil
		}
	}

	return nil, nil
}

// selectOldestChunkAtDepth scans context_items from oldest ordinal, collecting consecutive
// summaries at the given depth. Stops at non-summary items, different depth, fresh tail, or
// token overflow. Returns contiguous chunk of summaries.
func (e *CompactionEngine) selectOldestChunkAtDepth(
	ctx context.Context, convID int64, targetDepth int, forced bool,
) ([]Summary, error) {
	minFanout := condensedMinFanout(forced)

	items, err := e.store.GetContextItems(ctx, convID)
	if err != nil {
		return nil, err
	}

	tailStartIdx := len(items) - FreshTailCount
	if tailStartIdx < 0 {
		tailStartIdx = 0
	}

	var chunk []Summary
	accumTokens := 0

	for i := 0; i < tailStartIdx; i++ {
		item := items[i]
		if item.ItemType != "summary" {
			// Non-summary breaks the chunk
			break
		}
		sum, err := e.store.GetSummary(ctx, item.SummaryID)
		if err != nil {
			break
		}
		if sum.Depth != targetDepth {
			// Different depth breaks the chunk
			break
		}
		if sum.TokenCount > LeafChunkTokens {
			// Skip an oversized summary instead of letting it block every
			// older candidate.
			continue
		}
		if accumTokens+sum.TokenCount > LeafChunkTokens {
			// Token overflow stops collection
			break
		}
		chunk = append(chunk, *sum)
		accumTokens += sum.TokenCount
	}

	// Min tokens check: spec line 808
	// chunk tokens must be >= max(CondensedTargetTokens, LeafChunkTokens × 0.1) = 2000
	minTokens := CondensedTargetTokens // 2000
	if len(chunk) < minFanout || accumTokens < minTokens {
		return nil, nil
	}

	return chunk, nil
}

// generateLeafSummary calls the LLM to generate a leaf summary with 3-level escalation.
// Level 1: normal LLM prompt. Level 2: aggressive prompt. Level 3: deterministic truncation.
func (e *CompactionEngine) generateLeafSummary(
	ctx context.Context,
	messages []Message,
	previousSummary string,
	instructions string,
) (string, error) {
	if e.complete == nil {
		return truncateSummary(messages), nil
	}

	sourceText := formatMessagesForSummary(messages)
	inputTokens := sumMessageTokens(messages)
	targetTokens := min(LeafTargetTokens, int(float64(inputTokens)*0.35))

	// Level 1: normal prompt
	prompt := withFocus(
		buildLeafSummaryPrompt(sourceText, previousSummary, targetTokens),
		instructions,
	)
	content, err := e.complete(ctx, prompt, CompleteOptions{
		MaxTokens:   LeafTargetTokens * 2,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}
	if content == "" {
		// Retry with temperature=0
		content, err = e.complete(ctx, prompt, CompleteOptions{
			MaxTokens:   LeafTargetTokens * 2,
			Temperature: 0,
		})
		if err != nil {
			return "", err
		}
	}

	// Level 1 only succeeds if it actually reaches the requested target size.
	if content != "" &&
		tokenizer.EstimateMessageTokens(providers.Message{Content: content}) <= targetTokens {
		return content, nil
	}

	// Level 2: aggressive prompt
	aggressiveTarget := min(640, int(float64(inputTokens)*0.20))
	aggressivePrompt := withFocus(
		buildAggressiveLeafSummaryPrompt(sourceText, previousSummary, aggressiveTarget),
		instructions,
	)
	content, err = e.complete(ctx, aggressivePrompt, CompleteOptions{
		MaxTokens:   aggressiveTarget * 2,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}
	if content == "" {
		// Retry with temperature=0
		content, err = e.complete(ctx, aggressivePrompt, CompleteOptions{
			MaxTokens:   aggressiveTarget * 2,
			Temperature: 0,
		})
		if err != nil {
			return "", err
		}
	}
	if content != "" &&
		tokenizer.EstimateMessageTokens(providers.Message{Content: content}) <= aggressiveTarget {
		return content, nil
	}

	// Level 3: deterministic truncation
	return truncateSummary(messages), nil
}

// generateCondensedSummary calls the LLM to generate a condensed summary with 3-level escalation.
func (e *CompactionEngine) generateCondensedSummary(
	ctx context.Context,
	summaries []Summary,
	instructions string,
) (string, error) {
	if e.complete == nil {
		return truncateCondensedSummaries(summaries), nil
	}

	sourceText := formatSummariesForCondensation(summaries)
	inputTokens := sumSummaryTokens(summaries)
	targetTokens := min(CondensedTargetTokens, int(float64(inputTokens)*0.35))

	// Level 1: normal prompt
	prompt := withFocus(buildCondensedSummaryPrompt(sourceText, targetTokens), instructions)
	content, err := e.complete(ctx, prompt, CompleteOptions{
		MaxTokens:   CondensedTargetTokens * 2,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}
	if content == "" {
		content, err = e.complete(ctx, prompt, CompleteOptions{
			MaxTokens:   CondensedTargetTokens * 2,
			Temperature: 0,
		})
		if err != nil {
			return "", err
		}
	}
	if content != "" {
		return content, nil
	}

	// Level 2: aggressive prompt
	aggressiveTarget := min(640, int(float64(inputTokens)*0.20))
	aggressivePrompt := withFocus(
		buildCondensedSummaryPrompt(sourceText, aggressiveTarget),
		instructions,
	)
	content, err = e.complete(ctx, aggressivePrompt, CompleteOptions{
		MaxTokens:   aggressiveTarget * 2,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}
	if content != "" {
		return content, nil
	}

	// Level 3: deterministic fallback
	return truncateCondensedSummaries(summaries), nil
}

// runCondensedLoop runs condensed compaction in a loop until:
// a) context tokens <= threshold (success), OR
// b) No candidate found (nothing to condense), OR
// c) tokensAfter >= tokensBefore (no progress this iteration), OR
// d) tokensAfter >= previousTokens (no improvement over last iteration)
//
// It returns how many condensed summaries were created.
func (e *CompactionEngine) runCondensedLoop(
	ctx context.Context,
	convID int64,
	instructions string,
	budget int,
) (created int) {
	var prevTokens int
	for {
		select {
		case <-ctx.Done():
			return created
		default:
		}

		tokensBefore, err := e.store.GetContextTokenCount(ctx, convID)
		if err != nil {
			logger.ErrorCF(
				"seahorse",
				"condensed: get tokens",
				map[string]any{"error": err.Error()},
			)
			return created
		}
		if budget > 0 && tokensBefore <= budget {
			return created
		}

		condensedID, err := e.compactCondensed(ctx, convID, instructions, false)
		if err != nil {
			logger.ErrorCF("seahorse", "condensed: compact", map[string]any{"error": err.Error()})
			return created
		}
		if condensedID == nil {
			// No candidate found
			logger.DebugCF("seahorse", "condensed: no candidate", map[string]any{"conv_id": convID})
			return created
		}
		created++

		tokensAfter, _ := e.store.GetContextTokenCount(ctx, convID)

		if tokensAfter >= tokensBefore {
			// No progress this iteration
			logger.DebugCF(
				"seahorse",
				"condensed: no progress",
				map[string]any{
					"conv_id":       convID,
					"tokens_before": tokensBefore,
					"tokens_after":  tokensAfter,
				},
			)
			return created
		}
		if tokensAfter >= prevTokens && prevTokens > 0 {
			// No improvement over last iteration
			logger.DebugCF(
				"seahorse",
				"condensed: no improvement",
				map[string]any{"conv_id": convID, "tokens": tokensAfter},
			)
			return created
		}

		prevTokens = tokensAfter
	}
}

// --- Helper functions ---

func formatMessagesForSummary(messages []Message) string {
	var result string
	for _, m := range messages {
		ts := m.CreatedAt.Format("2006-01-02 15:04 MST")
		content := m.Content
		if content == "" && len(m.Parts) > 0 {
			content = partsToReadableContent(m.Parts)
		}
		result += fmt.Sprintf("[%s]\n%s\n\n", ts, content)
	}
	return result
}

func formatSummariesForCondensation(summaries []Summary) string {
	var result string
	for _, s := range summaries {
		earliest := ""
		if s.EarliestAt != nil {
			earliest = s.EarliestAt.Format("2006-01-02")
		}
		latest := ""
		if s.LatestAt != nil {
			latest = s.LatestAt.Format("2006-01-02")
		}
		result += fmt.Sprintf("[%s - %s]\n%s\n\n", earliest, latest, s.Content)
	}
	return result
}

// withFocus appends the user's /compact focus to a summary prompt. It is a no-op
// for empty instructions, so automatic compaction prompts are unchanged.
func withFocus(prompt, instructions string) string {
	if instructions == "" {
		return prompt
	}
	return prompt + "\n\n<focus>\n" + instructions + "\n</focus>\n" +
		"Prioritize preserving details related to the focus above. " +
		"All output requirements above still apply."
}

func buildLeafSummaryPrompt(sourceText, previousSummary string, targetTokens int) string {
	prev := "(none)"
	if previousSummary != "" {
		prev = previousSummary
	}
	return fmt.Sprintf(`You summarize a SEGMENT of a conversation for future model turns.
Treat this as incremental memory compaction input, not a full-conversation summary.

Normal summary policy:
- Preserve key decisions, rationale, constraints, and active tasks.
- Keep essential technical details needed to continue work safely.
- Remove obvious repetition and conversational filler.

Output requirements:
- Plain text only.
- No preamble, headings, or markdown formatting.
- Track file operations (created, modified, deleted, renamed) with file paths and current status.
- If no file operations appear, include exactly: "Files: none".
- End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
- Target length: about %d tokens or less.

<previous_context>
%s
</previous_context>

<conversation_segment>
%s
</conversation_segment>`, targetTokens, prev, sourceText)
}

func buildCondensedSummaryPrompt(sourceText string, targetTokens int) string {
	return fmt.Sprintf(`You condense multiple summaries into a single higher-level summary.
Preserve all important decisions, constraints, and outcomes.
Merge overlapping topics. Keep technical details intact.

Output requirements:
- Plain text only.
- No preamble, headings, or markdown formatting.
- End with exactly: "Expand for details about: <comma-separated list>".
- Target length: about %d tokens or less.

<summaries>
%s
</summaries>`, targetTokens, sourceText)
}

func buildAggressiveLeafSummaryPrompt(sourceText, previousSummary string, targetTokens int) string {
	prev := "(none)"
	if previousSummary != "" {
		prev = previousSummary
	}
	return fmt.Sprintf(`You summarize a SEGMENT of a conversation for future model turns.
Aggressive summary policy:
- Keep only durable facts and current task state.
- Remove examples, repetition, and low-value narrative details.
- Preserve explicit TODOs, blockers, decisions, and constraints.

Output requirements:
- Plain text only.
- No preamble, headings, or markdown formatting.
- Track file operations (created, modified, deleted, renamed) with file paths and current status.
- If no file operations appear, include exactly: "Files: none".
- End with exactly: "Expand for details about: <comma-separated list of what was dropped or compressed>".
- Target length: about %d tokens or less.

<previous_context>
%s
</previous_context>

<conversation_segment>
%s
</conversation_segment>`, targetTokens, prev, sourceText)
}

// maxTruncatedSummaryBytes bounds the deterministic fallback summary size.
const maxTruncatedSummaryBytes = 2048

// truncateUTF8Bytes cuts s to at most maxBytes bytes without splitting a UTF-8
// rune, so the result is always valid UTF-8. CJK text is 3 bytes per rune, so a
// naive byte slice would leave a dangling partial rune.
func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func truncateSummary(messages []Message) string {
	var full strings.Builder
	for _, m := range messages {
		full.WriteString(messageSummaryText(m))
		full.WriteByte('\n')
	}
	if full.Len() <= maxTruncatedSummaryBytes {
		return full.String() + fmt.Sprintf("\n[Truncated from %d messages]", len(messages))
	}

	// The deterministic fallback used to keep only the oldest 2048 bytes,
	// which could drop the user's latest instruction. Keep the newest user
	// message and the assistant's latest task state first, then the newest
	// tool results, and fill the rest with older context.
	marker := fmt.Sprintf("\n[Truncated from %d messages]", len(messages))
	budget := maxTruncatedSummaryBytes - len(marker)
	if budget < 0 {
		budget = 0
	}

	var out strings.Builder
	used := make(map[int]bool)
	write := func(i, limit int) {
		if i < 0 || i >= len(messages) || used[i] || limit <= 0 {
			return
		}
		text := strings.TrimSpace(messageSummaryText(messages[i]))
		if text == "" {
			return
		}
		prefix := ""
		if out.Len() > 0 {
			prefix = "\n"
		}
		limit -= len(prefix)
		if limit <= 0 {
			return
		}
		text = truncateUTF8Bytes(messages[i].Role+": "+text, limit)
		if text == "" {
			return
		}
		out.WriteString(prefix)
		out.WriteString(text)
		used[i] = true
	}

	lastUser, lastAssistant := -1, -1
	var lastTools []int
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messageSummaryText(messages[i])) == "" {
			continue
		}
		switch messages[i].Role {
		case "user":
			if lastUser < 0 {
				lastUser = i
			}
		case "assistant":
			if lastAssistant < 0 {
				lastAssistant = i
			}
		case "tool":
			if len(lastTools) < 3 {
				lastTools = append(lastTools, i)
			}
		}
		if lastUser >= 0 && lastAssistant >= 0 && len(lastTools) >= 3 {
			break
		}
	}

	userBudget := budget * 2 / 5
	assistantBudget := budget / 4
	toolBudget := budget - userBudget - assistantBudget
	write(lastUser, userBudget)
	write(lastAssistant, assistantBudget)
	if len(lastTools) > 0 {
		perTool := toolBudget / len(lastTools)
		extra := toolBudget % len(lastTools)
		for i, idx := range lastTools {
			limit := perTool
			if i < extra {
				limit++
			}
			write(idx, limit)
		}
	}

	// Fill leftover space with the oldest remaining messages.
	remaining := budget - out.Len()
	for i := 0; i < len(messages) && remaining > 0; i++ {
		if used[i] {
			continue
		}
		before := out.Len()
		write(i, remaining)
		if out.Len() == before {
			continue
		}
		remaining = budget - out.Len()
	}

	out.WriteString(marker)
	return out.String()
}

func messageSummaryText(m Message) string {
	if m.Content != "" {
		return m.Content
	}
	if len(m.Parts) > 0 {
		return partsToReadableContent(m.Parts)
	}
	return ""
}

func truncateCondensedSummaries(summaries []Summary) string {
	content := ""
	for _, s := range summaries {
		content += s.Content + "\n"
	}
	content = truncateUTF8Bytes(content, maxTruncatedSummaryBytes)
	content += fmt.Sprintf("\n[Condensed from %d summaries]", len(summaries))
	return content
}

func sumMessageTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += m.TokenCount
	}
	return total
}

func sumSummaryTokens(summaries []Summary) int {
	total := 0
	for _, s := range summaries {
		total += s.TokenCount
	}
	return total
}
